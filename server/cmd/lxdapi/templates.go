package main

import (
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"lxdapi/pkg/logger"
)

// loadTemplates 加载 HTML 模板。
// 若 dir 为空或不可用，则回退到内置 embed 模板。
// 若 dir 不为空：首次运行（目录不存在或为空）会把内置模板导出到该目录，
// 之后从磁盘递归加载，方便直接改文件、无需重新编译。
func loadTemplates(dir string) (*template.Template, error) {
	if dir == "" {
		return loadEmbeddedTemplates()
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		logger.Error("模板目录解析失败，回退内置: %v", err)
		return loadEmbeddedTemplates()
	}

	if _, statErr := os.Stat(absDir); os.IsNotExist(statErr) {
		if mkErr := os.MkdirAll(absDir, 0o755); mkErr != nil {
			logger.Error("创建模板目录失败，回退内置: %v", mkErr)
			return loadEmbeddedTemplates()
		}
		exportEmbeddedTemplates(absDir)
	} else if statErr == nil {
		if empty, _ := isDirEmpty(absDir); empty {
			exportEmbeddedTemplates(absDir)
		}
	}

	tmpl := template.New("").Funcs(template.FuncMap{})
	walkErr := filepath.WalkDir(absDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		rel, relErr := filepath.Rel(absDir, path)
		if relErr != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			logger.Error("读取模板失败 %s: %v", path, readErr)
			return nil
		}
		if _, parseErr := tmpl.New(name).Parse(string(content)); parseErr != nil {
			logger.Error("解析模板失败 %s: %v", path, parseErr)
		}
		return nil
	})
	if walkErr != nil {
		logger.Error("遍历模板目录出错: %v", walkErr)
	}

	if len(tmpl.Templates()) == 0 {
		logger.Warn("磁盘模板为空，回退内置模板")
		return loadEmbeddedTemplates()
	}
	return tmpl, nil
}

// loadEmbeddedTemplates 从 embed 文件系统加载内置模板。
func loadEmbeddedTemplates() (*template.Template, error) {
	tmpl := template.New("").Funcs(template.FuncMap{})
	templatesFS, err := fs.Sub(embeddedFiles, "templates")
	if err != nil {
		return nil, err
	}
	fs.WalkDir(templatesFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		content, readErr := fs.ReadFile(templatesFS, path)
		if readErr != nil {
			logger.Error("读取嵌入式模板失败 %s: %v", path, readErr)
			return nil
		}
		name := filepath.ToSlash(path)
		if _, parseErr := tmpl.New(name).Parse(string(content)); parseErr != nil {
			logger.Error("解析嵌入式模板失败 %s: %v", path, parseErr)
		}
		return nil
	})
	return tmpl, nil
}

// exportEmbeddedTemplates 把内置模板导出到磁盘目录，方便在线/外部编辑。
func exportEmbeddedTemplates(dir string) {
	templatesFS, err := fs.Sub(embeddedFiles, "templates")
	if err != nil {
		logger.Error("无法读取内置模板用于导出: %v", err)
		return
	}
	fs.WalkDir(templatesFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		content, readErr := fs.ReadFile(templatesFS, path)
		if readErr != nil {
			return nil
		}
		dst := filepath.Join(dir, filepath.FromSlash(path))
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			logger.Error("创建模板子目录失败 %s: %v", dst, mkErr)
			return nil
		}
		if wErr := os.WriteFile(dst, content, 0o644); wErr != nil {
			logger.Error("导出模板失败 %s: %v", dst, wErr)
		}
		return nil
	})
	logger.OK("已将内置模板导出到 %s（可直接编辑，热加载生效）", dir)
}

// isDirEmpty 判断目录是否为空。
func isDirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// watchTemplates 轮询模板目录，文件变更后热重载并调用 apply 应用新模板。
// 采用 mtime 轮询而非 inotify，避免某些容器/overlay 环境下事件不可靠导致热加载失效。
func watchTemplates(dir string, apply func(*template.Template)) {
	if dir == "" || apply == nil {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		logger.Error("模板热加载目录解析失败: %v", err)
		return
	}
	last := collectMaxMtime(absDir)
	logger.OK("模板热加载已启用(轮询): %s", absDir)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			cur := collectMaxMtime(absDir)
			if cur == last {
				continue
			}
			last = cur
			logger.Info("检测到模板变更，重新加载...")
			newTmpl, reloadErr := loadTemplates(dir)
			if reloadErr != nil {
				logger.Error("重新加载模板失败: %v", reloadErr)
				continue
			}
			apply(newTmpl)
			logger.OK("模板热加载完成")
		}
	}()
}

// collectMaxMtime 返回目录下所有 .html 文件的最大修改时间（Unix 秒），无文件返回 0。
func collectMaxMtime(dir string) int64 {
	var max int64
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			if ms := info.ModTime().Unix(); ms > max {
				max = ms
			}
		}
		return nil
	})
	return max
}
