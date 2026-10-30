package lxc

import (
	"strings"
	"testing"
)

// 样例结构对齐 `lxc image list images: --format json` 的真实输出：
// 同一发行版有 amd64/arm64 两条、还有 cloud 等变体条目。
const sampleRemoteCatalog = `[
  {
    "aliases": [{"name": "ubuntu/24.04", "description": ""}, {"name": "ubuntu/noble", "description": ""}, {"name": "ubuntu/24.04/amd64", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "properties": {"architecture": "amd64", "description": "Ubuntu 24.04 LTS amd64 (release) (20261017)", "os": "Ubuntu", "release": "24.04", "variant": "default"},
    "public": true, "size": 268435456, "type": "container"
  },
  {
    "aliases": [{"name": "ubuntu/24.04/arm64", "description": ""}],
    "architecture": "aarch64",
    "fingerprint": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    "properties": {"architecture": "arm64", "description": "Ubuntu 24.04 LTS arm64 (release) (20261017)", "os": "Ubuntu", "release": "24.04", "variant": "default"},
    "public": true, "size": 251658240, "type": "container"
  },
  {
    "aliases": [{"name": "ubuntu/24.04/cloud", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
    "properties": {"architecture": "amd64", "description": "Ubuntu 24.04 LTS amd64 (cloud) (20261017)", "os": "Ubuntu", "release": "24.04", "variant": "cloud"},
    "public": true, "size": 300000000, "type": "container"
  },
  {
    "aliases": [{"name": "ubuntu/22.04", "description": ""}, {"name": "ubuntu/jammy", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
    "properties": {"architecture": "amd64", "description": "Ubuntu 22.04 LTS amd64 (release) (20261017)", "os": "Ubuntu", "release": "22.04", "variant": "default"},
    "public": true, "size": 240000000, "type": "container"
  },
  {
    "aliases": [{"name": "debian/13", "description": ""}, {"name": "debian/trixie", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
    "properties": {"architecture": "amd64", "description": "Debian 13 amd64 (release) (20261017)", "os": "Debian", "release": "13", "variant": "default"},
    "public": true, "size": 180000000, "type": "container"
  },
  {
    "aliases": [{"name": "rockylinux/9", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
    "properties": {"architecture": "amd64", "description": "Rocky Linux 9 amd64 (release) (20261017)", "os": "Rocky Linux", "release": "9", "variant": "default"},
    "public": true, "size": 200000000, "type": "container"
  },
  {
    "aliases": [{"name": "almalinux/9", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "1111111111111111111111111111111111111111111111111111111111111111",
    "properties": {"architecture": "amd64", "description": "AlmaLinux 9 amd64 (release) (20261017)", "os": "AlmaLinux", "release": "9", "variant": "default"},
    "public": true, "size": 200000000, "type": "container"
  },
  {
    "aliases": [{"name": "alpine/3.22", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "2222222222222222222222222222222222222222222222222222222222222222",
    "properties": {"architecture": "amd64", "description": "Alpine 3.22 amd64 (release) (20261017)", "os": "Alpine", "release": "3.22", "variant": "default"},
    "public": true, "size": 8000000, "type": "container"
  }
]`

func TestParseRemoteCatalog(t *testing.T) {
	images, err := ParseRemoteCatalog([]byte(sampleRemoteCatalog), "images")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	// 8 条原始记录：ubuntu/24.04 双架构去重为 1、cloud 变体滤除 → 6 条
	if len(images) != 6 {
		t.Fatalf("期望 6 条发行版，实际 %d 条: %+v", len(images), images)
	}

	byAlias := make(map[string]RemoteImage)
	for _, img := range images {
		byAlias[img.Alias] = img
		if strings.Contains(img.Description, "(cloud)") {
			t.Errorf("cloud 变体不应出现在列表中: %+v", img)
		}
		if img.Remote != "images" {
			t.Errorf("条目应带来源 remote=images，实际 %q: %+v", img.Remote, img)
		}
	}

	u2404, ok := byAlias["ubuntu/24.04"]
	if !ok {
		t.Fatalf("缺少 ubuntu/24.04: %+v", images)
	}
	if u2404.Name != "Ubuntu 24.04 LTS" {
		t.Errorf("ubuntu/24.04 易读名期望 %q，实际 %q", "Ubuntu 24.04 LTS", u2404.Name)
	}
	if u2404.Architecture != "amd64" {
		t.Errorf("去重后应优先保留 amd64 条目元数据，实际架构 %q", u2404.Architecture)
	}

	if got := byAlias["ubuntu/22.04"].Name; got != "Ubuntu 22.04 LTS" {
		t.Errorf("ubuntu/22.04 易读名期望 %q，实际 %q", "Ubuntu 22.04 LTS", got)
	}
	if got := byAlias["debian/13"].Name; got != "Debian 13" {
		t.Errorf("debian/13 易读名期望 %q，实际 %q", "Debian 13", got)
	}
	if got := byAlias["rockylinux/9"].Name; got != "Rocky Linux 9" {
		t.Errorf("rockylinux/9 易读名期望 %q，实际 %q", "Rocky Linux 9", got)
	}
	if got := byAlias["almalinux/9"].Name; got != "AlmaLinux 9" {
		t.Errorf("almalinux/9 易读名期望 %q，实际 %q", "AlmaLinux 9", got)
	}
	if got := byAlias["alpine/3.22"].Name; got != "Alpine 3.22" {
		t.Errorf("alpine/3.22 易读名期望 %q，实际 %q", "Alpine 3.22", got)
	}

	// 排序：Ubuntu 新版本在最前、Debian 其次
	if images[0].Alias != "ubuntu/24.04" || images[1].Alias != "ubuntu/22.04" {
		t.Errorf("排序期望 ubuntu/24.04、ubuntu/22.04 在前，实际 %s、%s", images[0].Alias, images[1].Alias)
	}
	if images[2].Alias != "debian/13" {
		t.Errorf("排序期望 debian/13 第三，实际 %s", images[2].Alias)
	}
}

// images: 源现状样例：Debian 的 release 是代号（trixie/bookworm/forky/bullseye/sid），
// Rocky 的 os 是 RockyLinux（无空格），CentOS 的 release 形如 9-Stream。
const sampleImagesCatalogCodename = `[
  {
    "aliases": [{"name": "debian/13", "description": ""}, {"name": "debian/trixie", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1",
    "properties": {"architecture": "amd64", "description": "Debian trixie amd64 (release) (20261017)", "os": "Debian", "release": "trixie", "variant": "default"},
    "public": true, "size": 190000000, "type": "container"
  },
  {
    "aliases": [{"name": "debian/12", "description": ""}, {"name": "debian/bookworm", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2",
    "properties": {"architecture": "amd64", "description": "Debian bookworm amd64 (release) (20261017)", "os": "Debian", "release": "bookworm", "variant": "default"},
    "public": true, "size": 180000000, "type": "container"
  },
  {
    "aliases": [{"name": "debian/14", "description": ""}, {"name": "debian/forky", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3",
    "properties": {"architecture": "amd64", "description": "Debian forky amd64 (release) (20261017)", "os": "Debian", "release": "forky", "variant": "default"},
    "public": true, "size": 195000000, "type": "container"
  },
  {
    "aliases": [{"name": "debian/11", "description": ""}, {"name": "debian/bullseye", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4",
    "properties": {"architecture": "amd64", "description": "Debian bullseye amd64 (release) (20261017)", "os": "Debian", "release": "bullseye", "variant": "default"},
    "public": true, "size": 170000000, "type": "container"
  },
  {
    "aliases": [{"name": "debian/sid", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5",
    "properties": {"architecture": "amd64", "description": "Debian sid amd64 (release) (20261017)", "os": "Debian", "release": "sid", "variant": "default"},
    "public": true, "size": 200000000, "type": "container"
  },
  {
    "aliases": [{"name": "rockylinux/9", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6",
    "properties": {"architecture": "amd64", "description": "Rocky Linux 9 amd64 (release) (20261017)", "os": "RockyLinux", "release": "9", "variant": "default"},
    "public": true, "size": 200000000, "type": "container"
  },
  {
    "aliases": [{"name": "centos/9-Stream", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7",
    "properties": {"architecture": "amd64", "description": "CentOS 9-Stream amd64 (release) (20261017)", "os": "CentOS", "release": "9-Stream", "variant": "default"},
    "public": true, "size": 210000000, "type": "container"
  },
  {
    "aliases": [{"name": "centos/10-Stream", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8a8",
    "properties": {"architecture": "amd64", "description": "CentOS 10-Stream amd64 (release) (20261017)", "os": "CentOS", "release": "10-Stream", "variant": "default"},
    "public": true, "size": 220000000, "type": "container"
  }
]`

func TestParseRemoteCatalogDisplayNames(t *testing.T) {
	images, err := ParseRemoteCatalog([]byte(sampleImagesCatalogCodename), "images")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	// 8 条原始记录：sid 被过滤 → 7 条
	if len(images) != 7 {
		t.Fatalf("期望 7 条（sid 已过滤），实际 %d 条: %+v", len(images), images)
	}

	byRel := make(map[string]RemoteImage)
	for _, img := range images {
		byRel[img.OS+"/"+img.Release] = img
		if strings.EqualFold(img.Release, "sid") {
			t.Errorf("Debian sid 不应出现在列表中: %+v", img)
		}
	}

	// Debian 代号 → 版本号显示；Release 与下载别名保持原样
	debianCases := map[string]struct{ name, alias string }{
		"trixie":   {"Debian 13", "debian/trixie"},
		"bookworm": {"Debian 12", "debian/bookworm"},
		"forky":    {"Debian 14", "debian/forky"},
		"bullseye": {"Debian 11", "debian/bullseye"},
	}
	for rel, want := range debianCases {
		img, ok := byRel["Debian/"+rel]
		if !ok {
			t.Fatalf("缺少 Debian %s 条目: %+v", rel, images)
		}
		if img.Name != want.name {
			t.Errorf("Debian %s 易读名期望 %q，实际 %q", rel, want.name, img.Name)
		}
		if img.Alias != want.alias {
			t.Errorf("Debian %s 下载别名应保持原别名 %q，实际 %q", rel, want.alias, img.Alias)
		}
	}

	// RockyLinux → Rocky Linux
	if got := byRel["RockyLinux/9"].Name; got != "Rocky Linux 9" {
		t.Errorf("RockyLinux 9 易读名期望 %q，实际 %q", "Rocky Linux 9", got)
	}

	// CentOS N-Stream → CentOS Stream N
	if got := byRel["CentOS/9-Stream"].Name; got != "CentOS Stream 9" {
		t.Errorf("CentOS 9-Stream 易读名期望 %q，实际 %q", "CentOS Stream 9", got)
	}
	if got := byRel["CentOS/10-Stream"].Name; got != "CentOS Stream 10" {
		t.Errorf("CentOS 10-Stream 易读名期望 %q，实际 %q", "CentOS Stream 10", got)
	}

	// 排序：Debian 在其他系统前，且按版本号降序（14 forky → 13 trixie → 12 bookworm → 11 bullseye），
	// 不能按代号字母序（字母序会把 forky 排到 trixie 后面）
	var debianOrder []string
	for _, img := range images {
		if img.OS == "Debian" {
			debianOrder = append(debianOrder, img.Release)
		}
	}
	wantOrder := []string{"forky", "trixie", "bookworm", "bullseye"}
	if len(debianOrder) != len(wantOrder) {
		t.Fatalf("Debian 条目数期望 %d，实际 %v", len(wantOrder), debianOrder)
	}
	for i, rel := range wantOrder {
		if debianOrder[i] != rel {
			t.Errorf("Debian 排序第 %d 位期望 %s，实际 %s（全序 %v）", i, rel, debianOrder[i], debianOrder)
		}
	}

	// CentOS 同系统内 10-Stream 应排在 9-Stream 前
	var centosOrder []string
	for _, img := range images {
		if img.OS == "CentOS" {
			centosOrder = append(centosOrder, img.Release)
		}
	}
	if len(centosOrder) != 2 || centosOrder[0] != "10-Stream" || centosOrder[1] != "9-Stream" {
		t.Errorf("CentOS 排序期望 [10-Stream 9-Stream]，实际 %v", centosOrder)
	}
}

// ubuntu: 源样例：别名是裸版本号/代号（无 ubuntu/ 前缀），含带架构后缀的别名。
const sampleUbuntuCatalog = `[
  {
    "aliases": [{"name": "24.04", "description": ""}, {"name": "noble", "description": ""}, {"name": "24.04/amd64", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1",
    "properties": {"architecture": "amd64", "description": "Ubuntu 24.04 LTS amd64 (release) (20261017)", "os": "Ubuntu", "release": "24.04", "variant": "default"},
    "public": true, "size": 268435456, "type": "container"
  },
  {
    "aliases": [{"name": "22.04", "description": ""}, {"name": "jammy", "description": ""}],
    "architecture": "x86_64",
    "fingerprint": "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2",
    "properties": {"architecture": "amd64", "description": "Ubuntu 22.04 LTS amd64 (release) (20261017)", "os": "Ubuntu", "release": "22.04", "variant": "default"},
    "public": true, "size": 240000000, "type": "container"
  }
]`

func TestParseRemoteCatalogUbuntuSource(t *testing.T) {
	images, err := ParseRemoteCatalog([]byte(sampleUbuntuCatalog), "ubuntu")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条: %+v", len(images), images)
	}

	u2404 := images[0]
	if u2404.Remote != "ubuntu" {
		t.Errorf("条目来源期望 ubuntu，实际 %q", u2404.Remote)
	}
	// 下载别名必须选数字版本别名 24.04，不能选代号 noble
	if u2404.Alias != "24.04" {
		t.Errorf("ubuntu: 源下载别名期望 %q，实际 %q", "24.04", u2404.Alias)
	}
	if u2404.Name != "Ubuntu 24.04 LTS" {
		t.Errorf("易读名期望 %q，实际 %q", "Ubuntu 24.04 LTS", u2404.Name)
	}
	if got := images[1].Alias; got != "22.04" {
		t.Errorf("ubuntu 22.04 下载别名期望 %q，实际 %q", "22.04", got)
	}
}

// ubuntu: 源真实样例（cloud-images.ubuntu.com/releases 官方流）：
// properties.os 是小写 ubuntu、release 是代号（不是版本号），别名含数字版/单字母/代号三种形态，
// 同一版本混有 container 与 virtual-machine 两型、多个构建日期与多架构。
// 24.04 组内：旧 amd64 构建、vm 型（最新日期也必须跳过）、arm64（日期更新也不能压过 amd64）、
// 新 amd64 构建（最终应保留这一条的元数据）。
const sampleUbuntuReleasesCatalog = `[
  {
    "aliases": [{"name": "24.04", "description": ""}, {"name": "n", "description": ""}, {"name": "noble", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2025-04-10T05:24:00Z",
    "fingerprint": "ubuntu2404-amd64-20250410",
    "properties": {"architecture": "amd64", "description": "Ubuntu 24.04 LTS amd64 (release) (20250410)", "filename": "ubuntu-24.04-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "noble"},
    "public": true, "size": 260000000, "type": "container"
  },
  {
    "aliases": [{"name": "24.04", "description": ""}, {"name": "n", "description": ""}, {"name": "noble", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2026-10-19T05:24:00Z",
    "fingerprint": "ubuntu2404-amd64-vm-20261019",
    "properties": {"architecture": "amd64", "description": "Ubuntu 24.04 LTS amd64 (release) (20261019)", "filename": "ubuntu-24.04-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "noble"},
    "public": true, "size": 900000000, "type": "virtual-machine"
  },
  {
    "aliases": [{"name": "24.04", "description": ""}, {"name": "n", "description": ""}, {"name": "noble", "description": ""}],
    "architecture": "aarch64",
    "created_at": "2026-10-18T05:24:00Z",
    "fingerprint": "ubuntu2404-arm64-20261018",
    "properties": {"architecture": "arm64", "description": "Ubuntu 24.04 LTS arm64 (release) (20261018)", "filename": "ubuntu-24.04-server-cloudimg-arm64-lxd.tar.xz", "os": "ubuntu", "release": "noble"},
    "public": true, "size": 250000000, "type": "container"
  },
  {
    "aliases": [{"name": "24.04", "description": ""}, {"name": "n", "description": ""}, {"name": "noble", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2026-10-17T05:24:00Z",
    "fingerprint": "ubuntu2404-amd64-20261017",
    "properties": {"architecture": "amd64", "description": "Ubuntu 24.04 LTS amd64 (release) (20261017)", "filename": "ubuntu-24.04-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "noble"},
    "public": true, "size": 268435456, "type": "container"
  },
  {
    "aliases": [{"name": "24.10", "description": ""}, {"name": "o", "description": ""}, {"name": "oracular", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2026-10-17T05:24:00Z",
    "fingerprint": "ubuntu2410-amd64-20261017",
    "properties": {"architecture": "amd64", "description": "Ubuntu 24.10 amd64 (release) (20261017)", "filename": "ubuntu-24.10-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "oracular"},
    "public": true, "size": 270000000, "type": "container"
  },
  {
    "aliases": [{"name": "22.04", "description": ""}, {"name": "j", "description": ""}, {"name": "jammy", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2026-10-17T05:24:00Z",
    "fingerprint": "ubuntu2204-amd64-20261017",
    "properties": {"architecture": "amd64", "description": "Ubuntu 22.04 LTS amd64 (release) (20261017)", "filename": "ubuntu-22.04-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "jammy"},
    "public": true, "size": 240000000, "type": "container"
  },
  {
    "aliases": [{"name": "20.04", "description": ""}, {"name": "f", "description": ""}, {"name": "focal", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2026-10-17T05:24:00Z",
    "fingerprint": "ubuntu2004-amd64-20261017",
    "properties": {"architecture": "amd64", "description": "Ubuntu 20.04 LTS amd64 (release) (20261017)", "filename": "ubuntu-20.04-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "focal"},
    "public": true, "size": 230000000, "type": "container"
  },
  {
    "aliases": [{"name": "17.10", "description": ""}, {"name": "a", "description": ""}, {"name": "artful", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2017-10-19T05:24:00Z",
    "fingerprint": "ubuntu1710-amd64-20171019",
    "properties": {"architecture": "amd64", "description": "Ubuntu 17.10 amd64 (release) (20171019)", "filename": "ubuntu-17.10-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "artful"},
    "public": true, "size": 200000000, "type": "container"
  },
  {
    "aliases": [{"name": "zesty", "description": ""}],
    "architecture": "x86_64",
    "created_at": "2017-04-13T05:24:00Z",
    "fingerprint": "ubuntu1704-amd64-20170413",
    "properties": {"architecture": "amd64", "description": "Ubuntu 17.04 amd64 (release) (20170413)", "filename": "ubuntu-17.04-server-cloudimg-amd64-lxd.tar.xz", "os": "ubuntu", "release": "zesty"},
    "public": true, "size": 190000000, "type": "container"
  }
]`

func TestParseRemoteCatalogUbuntuReleases(t *testing.T) {
	images, err := ParseRemoteCatalog([]byte(sampleUbuntuReleasesCatalog), "ubuntu")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	// 9 条原始记录：vm 型跳过、17.10 与无数字别名的 zesty 滤除、24.04 四条去重为一 → 4 条
	if len(images) != 4 {
		t.Fatalf("期望 4 条，实际 %d 条: %+v", len(images), images)
	}

	// 排序：Ubuntu 组内按版本数值降序
	wantOrder := []string{"24.10", "24.04", "22.04", "20.04"}
	for i, rel := range wantOrder {
		if images[i].Release != rel {
			t.Fatalf("排序第 %d 位期望 %s，实际 %s（全序 %+v）", i, rel, images[i].Release, images)
		}
	}

	wantNames := map[string]string{
		"24.10": "Ubuntu 24.10", // 非 LTS（10 月版）
		"24.04": "Ubuntu 24.04 LTS",
		"22.04": "Ubuntu 22.04 LTS",
		"20.04": "Ubuntu 20.04 LTS",
	}
	for _, img := range images {
		if img.Remote != "ubuntu" {
			t.Errorf("条目来源期望 ubuntu，实际 %q: %+v", img.Remote, img)
		}
		if img.OS != "Ubuntu" {
			t.Errorf("条目 OS 期望固定为 Ubuntu，实际 %q: %+v", img.OS, img)
		}
		// 下载别名必须是数字版本别名
		if img.Alias != img.Release {
			t.Errorf("下载别名期望数字版本 %q，实际 %q", img.Release, img.Alias)
		}
		if img.Name != wantNames[img.Release] {
			t.Errorf("版本 %s 显示名期望 %q，实际 %q", img.Release, wantNames[img.Release], img.Name)
		}
		if img.Release == "17.10" || img.Release == "zesty" || img.Release == "noble" {
			t.Errorf("EOL/代号条目不应出现在列表中: %+v", img)
		}
	}

	// 24.04 去重：保留最新 amd64 container 构建的元数据（不是 vm、不是 arm64、不是旧构建）
	u2404 := images[1]
	if u2404.Fingerprint != "ubuntu2404-amd64-20261017" {
		t.Errorf("24.04 去重期望保留最新 amd64 构建，实际指纹 %q", u2404.Fingerprint)
	}
	if u2404.Architecture != "amd64" {
		t.Errorf("24.04 架构期望 amd64，实际 %q", u2404.Architecture)
	}
	if u2404.Description != "Ubuntu 24.04 LTS amd64 (release) (20261017)" {
		t.Errorf("24.04 描述期望取 properties.description，实际 %q", u2404.Description)
	}
}

func TestMergeRemoteImages(t *testing.T) {
	imagesList, err := ParseRemoteCatalog([]byte(sampleImagesCatalogCodename), "images")
	if err != nil {
		t.Fatalf("解析 images 样例失败: %v", err)
	}
	ubuntuList, err := ParseRemoteCatalog([]byte(sampleUbuntuCatalog), "ubuntu")
	if err != nil {
		t.Fatalf("解析 ubuntu 样例失败: %v", err)
	}

	merged := MergeRemoteImages(imagesList, ubuntuList)

	// images 源 7 条（sid 已过滤）+ ubuntu 源 2 条，无重叠 → 9 条
	if len(merged) != 9 {
		t.Fatalf("合并后期望 9 条，实际 %d 条: %+v", len(merged), merged)
	}

	// Ubuntu 24.04 LTS 必须出现、来源标记为 ubuntu、且排在最前
	first := merged[0]
	if first.Name != "Ubuntu 24.04 LTS" {
		t.Errorf("合并后第一条期望 Ubuntu 24.04 LTS，实际 %q", first.Name)
	}
	if first.Remote != "ubuntu" {
		t.Errorf("Ubuntu 条目来源期望 ubuntu，实际 %q", first.Remote)
	}
	if first.Alias != "24.04" {
		t.Errorf("Ubuntu 条目下载别名期望 24.04，实际 %q", first.Alias)
	}
	for _, img := range merged {
		if img.OS == "Debian" && img.Remote != "images" {
			t.Errorf("Debian 条目来源期望 images，实际 %q: %+v", img.Remote, img)
		}
	}

	// 同一发行版在两源都出现时只保留一条（先出现的源优先）
	dup := MergeRemoteImages(ubuntuList, ubuntuList)
	if len(dup) != len(ubuntuList) {
		t.Errorf("重复合并应去重：期望 %d 条，实际 %d 条", len(ubuntuList), len(dup))
	}
}
