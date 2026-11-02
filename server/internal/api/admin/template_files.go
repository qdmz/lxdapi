package admin

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/core"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/response"
	"lxdapi/pkg/utils"
)

// templateFilesDir 返回启用的外部模板目录绝对路径；未启用返回错误。
func templateFilesDir() (string, error) {
	dir := core.GlobalConfig.System.Server.TemplatesDir
	if dir == "" {
		return "", fmt.Errorf("未启用外部模板目录（templates_dir 为空），无法在线编辑")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// safeTemplatePath 将相对路径解析为目录内绝对路径，防止路径穿越。
func safeTemplatePath(dir, rel string) (string, error) {
	rel = filepath.Clean(rel)
	if rel == "" || rel == "." {
		return "", fmt.Errorf("路径为空")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("不允许绝对路径")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("不允许路径穿越")
	}
	target := filepath.Join(dir, rel)
	// 二次校验：target 必须严格位于 dir 之内
	if target != dir && !strings.HasPrefix(target, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("非法路径")
	}
	// 仅允许 .html 模板文件
	if filepath.Ext(target) != ".html" {
		return "", fmt.Errorf("仅允许编辑 .html 模板文件")
	}
	return target, nil
}

// ListTemplateFiles 列出外部模板目录下的所有 .html 文件（相对路径）
// @Router /api/admin/template-files [get]
func ListTemplateFiles(c *gin.Context) {
	dir, err := templateFilesDir()
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	var files []gin.H
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		info, _ := d.Info()
		files = append(files, gin.H{
			"path":    filepath.ToSlash(rel),
			"size":    info.Size(),
			"modTime": info.ModTime().Format("2006-01-02 15:04:05"),
		})
		return nil
	})
	response.Success(c, gin.H{"files": files, "dir": dir})
}

// GetTemplateFile 读取指定模板文件内容
// @Router /api/admin/template-files/content [get]
func GetTemplateFile(c *gin.Context) {
	dir, err := templateFilesDir()
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	rel := c.Query("path")
	target, err := safeTemplatePath(dir, rel)
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	content, err := os.ReadFile(target)
	if err != nil {
		response.Error(c, 500, "读取失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"path": filepath.ToSlash(rel), "content": string(content)})
}

// SaveTemplateFile 保存模板文件内容并立即触发热加载
// @Router /api/admin/template-files/save [post]
func SaveTemplateFile(c *gin.Context) {
	dir, err := templateFilesDir()
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	var req struct {
		Path    string `json:"path" binding:"required"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}
	target, err := safeTemplatePath(dir, req.Path)
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	if err := os.WriteFile(target, []byte(req.Content), 0o644); err != nil {
		response.Error(c, 500, "写入失败: "+err.Error())
		return
	}
	core.TriggerTemplateReload()
	logger.OK("在线保存模板: %s", req.Path)
	response.Success(c, gin.H{"message": "保存成功，模板已热加载"})
}

// ReloadTemplates 手动触发热加载
// @Router /api/admin/template-files/reload [post]
func ReloadTemplates(c *gin.Context) {
	core.TriggerTemplateReload()
	response.Success(c, gin.H{"message": "已触发模板热加载"})
}

// PreviewTemplateFile 用当前编辑内容渲染模板，返回 HTML 供前端预览（所见即所得）
// @Router /api/admin/template-files/preview [post]
func PreviewTemplateFile(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}
	relPath := req["path"]
	content := req["content"]
	if relPath == "" {
		response.Error(c, 400, "缺少 path")
		return
	}
	dir, err := templateFilesDir()
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	if _, err = safeTemplatePath(dir, relPath); err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	name := filepath.ToSlash(relPath)
	tmp := template.New("").Funcs(template.FuncMap{})
	// 用磁盘上真实模板构建一个全新模板集，确保 header/footer/品牌等可被正确渲染
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() || filepath.Ext(p) != ".html" {
			return nil
		}
		b, re := os.ReadFile(p)
		if re != nil {
			return nil
		}
		n := filepath.ToSlash(p[len(dir)+1:])
		tmp.New(n).Parse(string(b))
		return nil
	})
	tpl := tmp.Lookup(name)
	if tpl == nil {
		tpl = tmp.New(name)
	}
	if _, perr := tpl.Parse(content); perr != nil {
		response.Error(c, 400, "模板语法错误: "+perr.Error())
		return
	}
	w := httptest.NewRecorder()
	cc, _ := gin.CreateTestContext(w)
	cc.Request, _ = http.NewRequest("GET", "/admin/template-editor", nil)
	var buf bytes.Buffer
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("预览渲染异常: %v", r)
				buf.WriteString(fmt.Sprintf("\n<!-- 预览渲染异常（可能缺少服务端数据）: %v -->", r))
			}
		}()
		data := utils.MergeTemplateData(cc, gin.H{"title": relPath, "username": "admin"})
		_ = tmp.ExecuteTemplate(&buf, name, data)
	}()
	response.Success(c, gin.H{"html": buf.String()})
}
