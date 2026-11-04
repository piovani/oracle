package db

import (
	"oracle/config"

	"go.opentelemetry.io/otel"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func Connect(cfg config.DBConfig) (*gorm.DB, error) {
	conn, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := Trace(conn, otel.GetTracerProvider()); err != nil {
		return nil, err
	}
	return conn, nil
}
