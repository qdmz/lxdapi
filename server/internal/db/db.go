package db

import (
	"database/sql"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"lxdapi/internal/core"
	"lxdapi/models"
	_ "modernc.org/sqlite"
)

var DB *gorm.DB

func Init() error {
	var err error
	cfg := core.GlobalConfig.System.Database

	switch cfg.Type {
	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.MySQL.User,
			cfg.MySQL.Password,
			cfg.MySQL.Host,
			cfg.MySQL.Port,
			cfg.MySQL.Database,
		)
		DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	case "postgres":
		dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Postgres.Host,
			cfg.Postgres.Port,
			cfg.Postgres.User,
			cfg.Postgres.Password,
			cfg.Postgres.Database,
			cfg.Postgres.SSLMode,
		)
		DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	default:
		dsn := cfg.SQLite.Path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
		sqlDB, err := sql.Open("sqlite", dsn)
		if err != nil {
			return err
		}
		DB, err = gorm.Open(sqlite.Dialector{Conn: sqlDB}, &gorm.Config{})
	}

	if err != nil {
		return err
	}

	// 存储池唯一性从「name 全局唯一」升级为「(node_id, name) 复合唯一」：
	// 先删旧的单列唯一索引，AutoMigrate 会按新标签建复合索引；
	// 否则不同节点的同名池（如各节点的 default）会互相抢占同一行。
	if DB.Migrator().HasTable(&models.StoragePool{}) && DB.Migrator().HasIndex(&models.StoragePool{}, "idx_storage_pools_name") {
		_ = DB.Migrator().DropIndex(&models.StoragePool{}, "idx_storage_pools_name")
	}

	// 模板唯一性同理：从「fingerprint 全局唯一」升级为「(node_id, fingerprint) 复合唯一」，
	// 否则同一镜像在多个节点同步时会互相抢占同一行。
	if DB.Migrator().HasTable(&models.Template{}) && DB.Migrator().HasIndex(&models.Template{}, "idx_templates_fingerprint") {
		_ = DB.Migrator().DropIndex(&models.Template{}, "idx_templates_fingerprint")
	}

	err = DB.AutoMigrate(
		&models.Container{},
		&models.User{},
		&models.Traffic{},
		&models.PortMappingV4{},
		&models.PortMappingV6{},
		&models.NATConfigV4{},
		&models.NATConfigV6{},
		&models.IPv4Binding{},
		&models.IPv4Pool{},
		&models.IPv6Binding{},
		&models.IPv6Pool{},
		&models.Task{},
		&models.ContainerCredential{},
		&models.Template{},
		&models.BrandSettings{},
		&models.PortRangeConfig{},
		&models.IPPoolSettings{},
		&models.CacheAutoRefreshSettings{},
		&models.StoragePool{},
		&models.AccessToken{},
		&models.SystemSetting{},
		&models.Product{},
		&models.Plan{},
		&models.Node{},
		&models.Region{},
		&models.Order{},
		&models.PayConfig{},
		&models.Ticket{},
		&models.TicketMessage{},
		&models.Notification{},
		&models.BalanceLog{},
		&models.KnowledgeArticle{},
		&models.Coupon{},
		&models.UserCoupon{},
	)
	if err != nil {
		return err
	}

	// 为已有数据库补齐用户余额 / 自动续费字段（针对老库升级）
	migrateUserBalance(DB)
	return nil
}

// migrateUserBalance 确保 users 表包含余额与自动续费字段（针对老库升级）
func migrateUserBalance(gdb *gorm.DB) {
	type colDef struct {
		name string
		ddl  string
	}
	cols := []colDef{
		{"balance", "ALTER TABLE users ADD COLUMN balance REAL DEFAULT 0"},
		{"auto_renew_flag", "ALTER TABLE users ADD COLUMN auto_renew_flag INTEGER DEFAULT 0"},
	}
	for _, c := range cols {
		if !gdb.Migrator().HasColumn(&models.User{}, c.name) {
			if err := gdb.Exec(c.ddl).Error; err != nil {
				continue
			}
		}
	}
}
