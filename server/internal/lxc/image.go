package lxc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"lxdapi/pkg/logger"
)

type ImageInfo struct {
	Fingerprint  string                 `json:"fingerprint"`
	Aliases      []ImageAlias           `json:"aliases"`
	Architecture string                 `json:"architecture"`
	Type         string                 `json:"type"` // container / virtual-machine（远程目录解析时按型过滤）
	Properties   map[string]interface{} `json:"properties"`
	Public       bool                   `json:"public"`
	Size         int64                  `json:"size"`
	AutoUpdate   bool                   `json:"auto_update"`
	UploadedAt   string                 `json:"uploaded_at"`
	CreatedAt    string                 `json:"created_at"`
	// DisplayName 易读名称（别名 > 描述 > 系统名+版本），供管理页下拉/列表展示，
	// 免得管理员面对一长串指纹。仅展示用，开通仍以别名或指纹为准。
	DisplayName string `json:"display_name"`
}

// FillDisplayName 按「别名 → properties.description → properties.name → os+release → 指纹」
// 顺序生成易读名称。本地与远程（节点侧同代码）列表都会调用。
func (img *ImageInfo) FillDisplayName() {
	if img.DisplayName != "" {
		return
	}
	for _, a := range img.Aliases {
		if a.Name != "" {
			img.DisplayName = a.Name
			return
		}
	}
	prop := func(k string) string {
		if img.Properties == nil {
			return ""
		}
		if v, ok := img.Properties[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	if d := prop("description"); d != "" {
		img.DisplayName = d
		return
	}
	if n := prop("name"); n != "" {
		img.DisplayName = n
		return
	}
	if osName, rel := prop("os"), prop("release"); osName != "" {
		if rel != "" {
			img.DisplayName = osName + " " + rel
		} else {
			img.DisplayName = osName
		}
		return
	}
	img.DisplayName = img.Fingerprint
}

type ImageAlias struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (c *Client) ListImages(ctx context.Context) ([]ImageInfo, error) {
	if c.IsRemote() {
		return c.remoteListImages(ctx)
	}
	logger.Info("获取LXD镜像列表")

	cmd := []string{"image", "list", "--format", "json"}
	output, err := c.exec(ctx, cmd...)
	if err != nil {
		return nil, fmt.Errorf("获取镜像列表失败: %v", err)
	}

	var images []ImageInfo
	if err := json.Unmarshal([]byte(output), &images); err != nil {
		return nil, fmt.Errorf("解析镜像列表失败: %v", err)
	}
	for i := range images {
		images[i].FillDisplayName()
	}

	logger.OK("获取到 %d 个镜像", len(images))
	return images, nil
}

func (c *Client) DeleteImage(ctx context.Context, fingerprint string) error {
	if c.IsRemote() {
		return c.remoteDeleteImage(ctx, fingerprint)
	}
	logger.Info("删除镜像: %s", fingerprint)

	cmd := []string{"image", "delete", fingerprint}
	if _, err := c.exec(ctx, cmd...); err != nil {
		return fmt.Errorf("删除镜像失败: %v", err)
	}

	logger.OK("镜像删除成功: %s", fingerprint)
	return nil
}

func (c *Client) GetImageInfo(ctx context.Context, fingerprint string) (*ImageInfo, error) {
	if c.IsRemote() {
		return c.remoteGetImage(ctx, fingerprint)
	}
	logger.Info("获取镜像详情: %s", fingerprint)

	cmd := []string{"image", "show", fingerprint, "--format", "json"}
	output, err := c.exec(ctx, cmd...)
	if err != nil {
		return nil, fmt.Errorf("获取镜像详情失败: %v", err)
	}

	var info ImageInfo
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		return nil, fmt.Errorf("解析镜像详情失败: %v", err)
	}
	info.FillDisplayName()

	return &info, nil
}

// CopyImage 从远程镜像仓库下载（拷贝）一个镜像到本节点。
// remote 为源 remote 名（如 images / ubuntu），image 为镜像名/别名/指纹；
// image 本身含 ":" 时按完整源处理。alias 为下载后在本节点设置的主别名。
func (c *Client) CopyImage(ctx context.Context, remote, image, alias string, copyAliases, public bool) error {
	if c.IsRemote() {
		return c.remoteCopyImage(ctx, remote, image, alias, copyAliases, public)
	}
	src := image
	if !strings.Contains(src, ":") {
		if remote == "" {
			remote = "images"
		}
		src = remote + ":" + image
	}
	args := []string{"image", "copy", src, "local:"}
	if alias != "" {
		args = append(args, "--alias", alias)
	}
	if copyAliases {
		args = append(args, "--copy-aliases")
	}
	if public {
		args = append(args, "--public")
	}
	logger.Info("下载镜像: %s", src)
	if _, err := c.exec(ctx, args...); err != nil {
		return fmt.Errorf("下载镜像失败: %v", err)
	}
	logger.OK("镜像下载成功: %s", src)
	return nil
}

// RemoteImage 远程镜像仓库（images: / ubuntu: 等 simplestreams 源）里一条可下载的系统镜像，
// 已按发行版（os+release）去重、只保留默认变体，供后台模板页展示与下载，
// 效果对标 lxdcloud 节点镜像下拉（易读系统名，而非指纹）。
type RemoteImage struct {
	Alias        string `json:"alias"`        // 下载引用：远程别名（如 ubuntu/24.04），无别名时为指纹
	Remote       string `json:"remote"`       // 来源 remote 名（images / ubuntu），下载时按条目自带源下发
	Name         string `json:"name"`         // 易读名（如 Ubuntu 24.04 LTS / Debian 13 / Rocky Linux 9）
	OS           string `json:"os"`           // 系统名（镜像属性 os）
	Release      string `json:"release"`      // 版本（镜像属性 release；Debian 可能为代号，下载别名仍以原别名为准）
	Architecture string `json:"architecture"` // 架构（目录条目自身的架构，仅展示；下载时节点按自身架构取）
	Description  string `json:"description"`  // 原始描述
	Fingerprint  string `json:"fingerprint"`  // 远程镜像指纹（仅参考，下载走别名）
	Size         int64  `json:"size"`
}

// ListRemoteImages 列出远程镜像仓库中可下载的系统镜像（按发行版去重、易读名展示）。
// remote 为源名（默认 images，即 images.linuxcontainers.org）。
// 本机节点直接查本机 LXD 的远程源；远程节点经节点系统级 API 取同一结果。
func (c *Client) ListRemoteImages(ctx context.Context, remote string) ([]RemoteImage, error) {
	if remote == "" {
		remote = "images"
	}
	if c.IsRemote() {
		return c.remoteListRemoteImages(ctx, remote)
	}
	logger.Info("获取远程镜像列表: %s", remote)
	output, err := c.exec(ctx, "image", "list", remote+":", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("获取远程镜像列表失败: %v", err)
	}
	images, err := ParseRemoteCatalog([]byte(output), remote)
	if err != nil {
		return nil, err
	}
	logger.OK("远程镜像列表: %d 个发行版", len(images))
	return images, nil
}

// ParseRemoteCatalog 解析 `lxc image list <remote>: --format json` 的输出：
// 只留默认变体（variant 为空或 default，滤掉 cloud/desktop 等变体），
// 按 os+release 去重（同一发行版多架构只留一条，优先 amd64 条目的元数据），
// 并生成易读名与下载别名；每条记录打上来源 remote，下载时按条目自带源下发。
// Debian 的 sid（滚动不稳定版）从列表滤除。排序：Ubuntu、Debian 优先，其余按系统名；同系统新版本在前。
// ubuntu 源（cloud-images 官方流）结构与 images 源完全不同，走 parseUbuntuCatalog 专门规则。
func ParseRemoteCatalog(data []byte, remote string) ([]RemoteImage, error) {
	var raw []ImageInfo
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析远程镜像列表失败: %v", err)
	}
	if remote == "ubuntu" {
		return parseUbuntuCatalog(raw), nil
	}
	byKey := make(map[string]*RemoteImage)
	var order []string
	for _, img := range raw {
		prop := func(k string) string {
			if img.Properties == nil {
				return ""
			}
			if v, ok := img.Properties[k].(string); ok {
				return strings.TrimSpace(v)
			}
			return ""
		}
		if v := prop("variant"); v != "" && v != "default" {
			continue
		}
		osName, release := prop("os"), prop("release")
		alias := pickRemoteAlias(img.Aliases, release)
		if (osName == "" || release == "") && alias != "" {
			// 属性缺失时用「系统/版本」形式的一段式别名兜底推导
			if parts := strings.SplitN(alias, "/", 2); len(parts) == 2 {
				if osName == "" {
					osName = parts[0]
				}
				if release == "" {
					release = parts[1]
				}
			}
		}
		if osName == "" || release == "" {
			continue
		}
		if strings.EqualFold(osName, "debian") && strings.EqualFold(release, "sid") {
			// Debian sid 是滚动不稳定版，不给管理员做默认可下载系统
			continue
		}
		ref := alias
		if ref == "" {
			ref = img.Fingerprint
		}
		if ref == "" {
			continue
		}
		arch := prop("architecture")
		if arch == "" {
			arch = img.Architecture
		}
		item := &RemoteImage{
			Alias:        ref,
			Remote:       remote,
			Name:         remoteDisplayName(osName, release, prop("description")),
			OS:           osName,
			Release:      release,
			Architecture: arch,
			Description:  prop("description"),
			Fingerprint:  img.Fingerprint,
			Size:         img.Size,
		}
		key := strings.ToLower(osName) + "/" + strings.ToLower(release)
		if old, ok := byKey[key]; ok {
			if remoteArchRank(arch) >= remoteArchRank(old.Architecture) {
				continue
			}
			*old = *item
			continue
		}
		byKey[key] = item
		order = append(order, key)
	}
	out := make([]RemoteImage, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sortRemoteImages(out)
	return out, nil
}

// parseUbuntuCatalog 解析 ubuntu: 源（cloud-images.ubuntu.com/releases 官方流）的目录。
// 该源是全部历史构建的原始流（万级条目）：properties.release 是发行代号（如 noble）
// 而非版本号，同一版本还混有 container / virtual-machine 两型与大量历史构建日期，
// 通用逻辑按 os+release 分组会塌成一堆代号条目。这里按版本精选：
// 只留 container 型、别名含数字版本号（YY.MM）的条目，版本 >= 20.04，
// 同版本去重（优先 amd64 条目的元数据，其次 created_at 最新），
// 显示名统一为「Ubuntu <版本>」，LTS 版本（偶数年份的 04 版）补 LTS 后缀，
// 下载别名即数字版本别名（如 ubuntu:24.04）。
func parseUbuntuCatalog(raw []ImageInfo) []RemoteImage {
	type candidate struct {
		item      RemoteImage
		createdAt string
	}
	byVersion := make(map[string]*candidate)
	for _, img := range raw {
		if img.Type != "container" {
			continue
		}
		version := ""
		for _, a := range img.Aliases {
			if name := strings.TrimSpace(a.Name); isUbuntuVersionAlias(name) {
				version = name
				break
			}
		}
		if version == "" || !ubuntuVersionAtLeast(version, 20, 4) {
			continue
		}
		prop := func(k string) string {
			if img.Properties == nil {
				return ""
			}
			if v, ok := img.Properties[k].(string); ok {
				return strings.TrimSpace(v)
			}
			return ""
		}
		arch := prop("architecture")
		if arch == "" {
			arch = img.Architecture
		}
		item := RemoteImage{
			Alias:        version,
			Remote:       "ubuntu",
			Name:         ubuntuDisplayName(version),
			OS:           "Ubuntu",
			Release:      version,
			Architecture: arch,
			Description:  prop("description"),
			Fingerprint:  img.Fingerprint,
			Size:         img.Size,
		}
		if old, ok := byVersion[version]; ok {
			newRank, oldRank := remoteArchRank(arch), remoteArchRank(old.item.Architecture)
			if newRank > oldRank || (newRank == oldRank && img.CreatedAt <= old.createdAt) {
				continue
			}
			old.item = item
			old.createdAt = img.CreatedAt
			continue
		}
		byVersion[version] = &candidate{item: item, createdAt: img.CreatedAt}
	}
	out := make([]RemoteImage, 0, len(byVersion))
	for _, cand := range byVersion {
		out = append(out, cand.item)
	}
	sortRemoteImages(out)
	return out
}

// isUbuntuVersionAlias 是否为 ubuntu: 源的数字版本别名（YY.MM，如 24.04），
// 等价于正则 ^\d{2}\.\d{2}$；同条目的代号别名（noble）与单字母别名（n）不算。
func isUbuntuVersionAlias(s string) bool {
	if len(s) != 5 || s[2] != '.' {
		return false
	}
	for _, i := range [...]int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ubuntuVersionParts 拆数字版本别名 YY.MM 为 (年, 月)；入参已由 isUbuntuVersionAlias 校验。
func ubuntuVersionParts(version string) (int, int) {
	parts := strings.SplitN(version, ".", 2)
	year, _ := strconv.Atoi(parts[0])
	month, _ := strconv.Atoi(parts[1])
	return year, month
}

// ubuntuVersionAtLeast 数字版本是否 >= (year, month)，按数值比较。
func ubuntuVersionAtLeast(version string, year, month int) bool {
	y, m := ubuntuVersionParts(version)
	return y > year || (y == year && m >= month)
}

// ubuntuDisplayName ubuntu: 源显示名：Ubuntu <版本>，LTS 版本补后缀。
// LTS 规律：偶数年份的 04 版（20.04 / 22.04 / 24.04 …）。
func ubuntuDisplayName(version string) string {
	name := "Ubuntu " + version
	if year, month := ubuntuVersionParts(version); month == 4 && year%2 == 0 {
		name += " LTS"
	}
	return name
}

// MergeRemoteImages 合并多个源的远程镜像列表（如 images: 与 ubuntu:）：
// 按 os+release 去重（先出现的源优先），再按统一规则排序。
// Ubuntu 已从 images: 源下架、只在 ubuntu: 源里，两源合并后管理员才能一次看到全部可下载系统。
func MergeRemoteImages(lists ...[]RemoteImage) []RemoteImage {
	seen := make(map[string]bool)
	out := make([]RemoteImage, 0)
	for _, list := range lists {
		for _, img := range list {
			key := strings.ToLower(img.OS) + "/" + strings.ToLower(img.Release)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, img)
		}
	}
	sortRemoteImages(out)
	return out
}

// sortRemoteImages 远程镜像列表统一排序：Ubuntu、Debian 优先，其余按系统名；同系统新版本在前。
// 版本比较走 remoteSortRelease，Debian 代号先映射成版本号再比，避免 forky/trixie 按字母序排错。
func sortRemoteImages(images []RemoteImage) {
	sort.SliceStable(images, func(i, j int) bool {
		ri, rj := remoteOSRank(images[i].OS), remoteOSRank(images[j].OS)
		if ri != rj {
			return ri < rj
		}
		if images[i].OS != images[j].OS {
			return images[i].OS < images[j].OS
		}
		return remoteReleaseGreater(
			remoteSortRelease(images[i].OS, images[i].Release),
			remoteSortRelease(images[j].OS, images[j].Release))
	})
}

// pickRemoteAlias 从镜像别名里挑下载引用。候选为至多含一个 "/" 的别名
// （images: 源是 ubuntu/24.04 形式，ubuntu: 源是裸的 24.04 形式），按版本段挑选：
// 1) 版本段与 release 相同且为纯数字/数字点号形态（如 24.04，优先于 noble 这类代号别名）；
// 2) 版本段与 release 相同（任意形态，如 release 本身就是代号时的 debian/trixie，下载仍用原别名）；
// 3) 版本段为数字形态里最短的；4) 最短的候选。没有候选返回空（上层回退到指纹）。
func pickRemoteAlias(aliases []ImageAlias, release string) string {
	versionPart := func(name string) string {
		if i := strings.LastIndex(name, "/"); i >= 0 {
			return name[i+1:]
		}
		return name
	}
	var exact, numeric, shortest string
	for _, a := range aliases {
		name := strings.TrimSpace(a.Name)
		if name == "" || strings.Count(name, "/") > 1 {
			continue
		}
		ver := versionPart(name)
		if release != "" && strings.EqualFold(ver, release) {
			if isNumericVersion(ver) {
				return name
			}
			if exact == "" {
				exact = name
			}
			continue
		}
		if isNumericVersion(ver) && (numeric == "" || len(name) < len(numeric)) {
			numeric = name
		}
		if shortest == "" || len(name) < len(shortest) {
			shortest = name
		}
	}
	switch {
	case exact != "":
		return exact
	case numeric != "":
		return numeric
	default:
		return shortest
	}
}

// isNumericVersion 是否为纯数字/数字点号形态的版本号（如 13、24.04、3.22）。
func isNumericVersion(s string) bool {
	if s == "" {
		return false
	}
	hasDigit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '.':
		default:
			return false
		}
	}
	return hasDigit
}

// debianCodenameVersions Debian 发布代号 → 版本号。仅用于显示与排序，
// 下载别名仍用原始别名（debian/trixie 等），不改下载引用。
var debianCodenameVersions = map[string]string{
	"bullseye": "11",
	"bookworm": "12",
	"trixie":   "13",
	"forky":    "14",
}

// debianVersionFor 当 os 为 Debian 且 release 为已知代号时返回对应版本号。
func debianVersionFor(osName, release string) (string, bool) {
	if !strings.EqualFold(osName, "debian") {
		return "", false
	}
	v, ok := debianCodenameVersions[strings.ToLower(release)]
	return v, ok
}

// remoteOSDisplayName 系统显示名映射：镜像属性 os 的原始写法（如 RockyLinux）
// 规范成管理员习惯的写法（Rocky Linux），仅用于展示。
func remoteOSDisplayName(osName string) string {
	if strings.EqualFold(osName, "rockylinux") {
		return "Rocky Linux"
	}
	return osName
}

// remoteDisplayRelease 展示用版本：Debian 代号映射成版本号（trixie → 13，显示「Debian 13」）；
// release 形如 N-Stream 时拼成 Stream N（显示「CentOS Stream 9」）。其余原样。
func remoteDisplayRelease(osName, release string) string {
	if v, ok := debianVersionFor(osName, release); ok {
		return v
	}
	if strings.HasSuffix(release, "-Stream") {
		if n := strings.TrimSuffix(release, "-Stream"); isNumericVersion(n) {
			return "Stream " + n
		}
	}
	return release
}

// remoteSortRelease 排序用版本：Debian 代号先映射成版本号再比（否则 forky/trixie 按字母序排错）；
// 其余用原 release（如 CentOS 的 10-Stream / 9-Stream 按首段数值比较即正确）。
func remoteSortRelease(osName, release string) string {
	if v, ok := debianVersionFor(osName, release); ok {
		return v
	}
	return release
}

// remoteDisplayName 易读名：规范后的 os + 展示版本拼接（Ubuntu 24.04 / Debian 13 / Rocky Linux 9 / CentOS Stream 9），
// 描述里带 LTS 的（Ubuntu 长期支持版）补上 LTS 后缀。
func remoteDisplayName(osName, release, description string) string {
	if osName == "" {
		return description
	}
	name := remoteOSDisplayName(osName)
	if release != "" {
		name += " " + remoteDisplayRelease(osName, release)
	}
	if description != "" && strings.Contains(description, name+" LTS") {
		name += " LTS"
	}
	return name
}

// remoteArchRank 架构偏好（去重时留哪一条的元数据）：amd64 优先，其次 arm64。
func remoteArchRank(arch string) int {
	switch strings.ToLower(arch) {
	case "amd64", "x86_64":
		return 0
	case "arm64", "aarch64":
		return 1
	}
	return 2
}

// remoteOSRank 列表排序的系统优先级：Ubuntu、Debian 在前，其余按名称。
func remoteOSRank(osName string) int {
	switch strings.ToLower(osName) {
	case "ubuntu":
		return 0
	case "debian":
		return 1
	}
	return 2
}

// remoteReleaseGreater 版本号降序比较：按 . 和 - 切段逐段比（数值段按数值比）。
func remoteReleaseGreater(a, b string) bool {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' })
	}
	pa, pb := split(a), split(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea == nil && eb == nil {
			if na != nb {
				return na > nb
			}
			continue
		}
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return len(pa) > len(pb)
}

// ImportImage 从 URL 导入镜像文件（tar / squashfs 等 LXD 镜像包）到本节点。
func (c *Client) ImportImage(ctx context.Context, url, alias string, public bool) error {
	if c.IsRemote() {
		return c.remoteImportImage(ctx, url, alias, public)
	}
	args := []string{"image", "import", url}
	if alias != "" {
		args = append(args, "--alias", alias)
	}
	if public {
		args = append(args, "--public")
	}
	logger.Info("导入镜像: %s", url)
	if _, err := c.exec(ctx, args...); err != nil {
		return fmt.Errorf("导入镜像失败: %v", err)
	}
	logger.OK("镜像导入成功: %s", url)
	return nil
}

// PublishImage 把一个容器发布（生成）为新镜像。
func (c *Client) PublishImage(ctx context.Context, container, alias string, public bool, description string) error {
	if c.IsRemote() {
		return c.remotePublishImage(ctx, container, alias, public, description)
	}
	args := []string{"publish", container}
	if alias != "" {
		args = append(args, "--alias", alias)
	}
	if public {
		args = append(args, "--public")
	}
	if description != "" {
		args = append(args, "description="+description)
	}
	logger.Info("从容器生成镜像: %s", container)
	if _, err := c.exec(ctx, args...); err != nil {
		return fmt.Errorf("生成镜像失败: %v", err)
	}
	logger.OK("镜像生成成功: %s", container)
	return nil
}

// CreateImageAlias 给镜像新增别名。
func (c *Client) CreateImageAlias(ctx context.Context, fingerprint, name string) error {
	if c.IsRemote() {
		return c.remoteCreateImageAlias(ctx, fingerprint, name)
	}
	if _, err := c.exec(ctx, "image", "alias", "create", name, fingerprint); err != nil {
		return fmt.Errorf("新增镜像别名失败: %v", err)
	}
	return nil
}

// DeleteImageAlias 删除镜像别名。
func (c *Client) DeleteImageAlias(ctx context.Context, name string) error {
	if c.IsRemote() {
		return c.remoteDeleteImageAlias(ctx, name)
	}
	if _, err := c.exec(ctx, "image", "alias", "delete", name); err != nil {
		return fmt.Errorf("删除镜像别名失败: %v", err)
	}
	return nil
}
