package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	System  SystemConfig  `yaml:"system"`
	LXC     LXCConfig     `yaml:"lxc"`
	Traffic TrafficConfig `yaml:"traffic"`
	Task    TaskConfig    `yaml:"task"`
	Admin   AdminConfig   `yaml:"admin"`
	Plugins PluginsConfig `yaml:"plugins"`
}

type SystemConfig struct {
	Server   ServerConfig   `yaml:"server"`
	Security SecurityConfig `yaml:"security"`
	Logger   LoggerConfig   `yaml:"logger"`
	Database DatabaseConfig `yaml:"database"`
}

type ServerConfig struct {
	Host string    `yaml:"host"`
	Port int       `yaml:"port"`
	Mode string    `yaml:"mode"`
	TLS  TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	Enabled      bool   `yaml:"enabled"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	AutoGenerate bool   `yaml:"auto_generate"`
}

type SecurityConfig struct {
	APIHash string `yaml:"api_hash"`
}

type LoggerConfig struct {
	Level    string `yaml:"level"`
	Colorful bool   `yaml:"colorful"`
}

type LXCConfig struct {
	Bin            string `yaml:"bin"` // LXC 可执行文件名称，默认 "lxc"（兼容 LXD 统一 CLI 环境）
	Socket         string `yaml:"socket"`
	Timeout        int    `yaml:"timeout"`
	DefaultStorage string `yaml:"default_storage"`
}

type TrafficConfig struct {
	Enabled   bool `yaml:"enabled"`
	Interval  int  `yaml:"interval"`
	BatchSize int  `yaml:"batch_size"`
}

type TaskConfig struct {
	Enabled         bool        `yaml:"enabled"`
	Backend         string      `yaml:"backend"`
	Workers         int         `yaml:"workers"`
	QueueSize       int         `yaml:"queue_size"`
	Timeout         int         `yaml:"timeout"`
	AutoCleanupDays int         `yaml:"auto_cleanup_days"`
	Redis           RedisConfig `yaml:"redis"`
}

type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type DatabaseConfig struct {
	Type     string         `yaml:"type"`
	SQLite   SQLiteConfig   `yaml:"sqlite"`
	MySQL    MySQLConfig    `yaml:"mysql"`
	Postgres PostgresConfig `yaml:"postgres"`
}

type SQLiteConfig struct {
	Path string `yaml:"path"`
}

type MySQLConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
}

type PostgresConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	SSLMode  string `yaml:"sslmode"`
}

type PluginsConfig struct {
	Nginx PluginConfig `yaml:"nginx"`
}

type PluginConfig struct {
	Enabled bool `yaml:"enabled"`
}

type AdminConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	SessionSecret string `yaml:"session_secret"`
}

var GlobalConfig *Config

// configPath 记录 LoadConfig 实际加载的配置文件路径，供 SaveConfig 写回。
var configPath string

// configMu 保护配置文件的并发读写（管理员自助改名/改密时使用）。
var configMu sync.Mutex

func LoadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	GlobalConfig = &Config{}
	if err := yaml.Unmarshal(data, GlobalConfig); err != nil {
		return err
	}
	configPath = path
	return nil
}

// saveConfigLocked 将当前 GlobalConfig 写回配置文件（调用方须持有 configMu）。
// 采用临时文件 + rename 的原子替换，避免写一半损坏配置导致管理员无法登录。
func saveConfigLocked() error {
	if configPath == "" {
		return fmt.Errorf("配置路径未初始化")
	}
	data, err := yaml.Marshal(GlobalConfig)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(configPath), ".config.yaml.tmp")
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

// SaveConfig 将当前内存配置持久化到配置文件。
func SaveConfig() error {
	configMu.Lock()
	defer configMu.Unlock()
	return saveConfigLocked()
}

// UpdateAdminCredentials 修改管理员用户名和/或密码（空字符串表示不改该项），
// 并立即写回配置文件，重启后仍然生效。
func UpdateAdminCredentials(username, password string) error {
	configMu.Lock()
	defer configMu.Unlock()
	if GlobalConfig == nil {
		return fmt.Errorf("配置未加载")
	}
	if username != "" {
		GlobalConfig.Admin.Username = username
	}
	if password != "" {
		GlobalConfig.Admin.Password = password
	}
	return saveConfigLocked()
}
