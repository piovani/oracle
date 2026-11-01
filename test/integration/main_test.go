//go:build integration

// Package integration runs the oracle against a real Postgres: the quote store, the whole
// sync → history → strategy pipeline, and the compiled binary of every command.
//
// It needs the database from docker-compose.yml and is excluded from `go test ./...`:
//
//	docker compose up -d
//	make test-integration
//
// Every test creates its own throwaway database, so the data in the main one is never touched.
// B3 and Yahoo are still faked: the real services are slow, rate limited and change every day.
package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"oracle/config"
	"oracle/pkg/db"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	// admin is a connection to the main database, used only to create and drop test databases.
	admin  *gorm.DB
	dbConf config.DBConfig
	// binary is the oracle command, built once for the CLI tests.
	binary string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	log.SetOutput(io.Discard) // the sync logs every file it skips
	dbConf = config.Load().DB
	var err error
	admin, err = gorm.Open(postgres.Open(dbConf.DSN()), &gorm.Config{Logger: logger.Discard})
	if err == nil {
		err = ping(admin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: cannot reach Postgres at %s:%s (%v)\nstart it with `docker compose up -d`\n", dbConf.Host, dbConf.Port, err)
		return 1
	}

	dir, err := os.MkdirTemp("", "oracle-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	binary = filepath.Join(dir, "oracle")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "oracle/cmd/oracle")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: build oracle: %v\n", err)
		return 1
	}
	return m.Run()
}

func ping(g *gorm.DB) error {
	sqlDB, err := g.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

// newDatabase creates an empty database for the test and drops it at the end.
// It returns a connection made the same way the commands make theirs, and the database's config.
func newDatabase(t *testing.T) (*gorm.DB, config.DBConfig) {
	t.Helper()
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	cfg := dbConf
	cfg.Name = "oracle_it_" + hex.EncodeToString(suffix)

	if err := admin.Exec("CREATE DATABASE " + cfg.Name).Error; err != nil {
		t.Fatalf("create database: %v", err)
	}
	conn, err := db.Connect(cfg)
	if err != nil {
		t.Fatalf("connect to %s: %v", cfg.Name, err)
	}
	conn = conn.Session(&gorm.Session{Logger: logger.Discard})

	t.Cleanup(func() {
		if sqlDB, err := conn.DB(); err == nil {
			_ = sqlDB.Close()
		}
		// FORCE also ends connections left open by a command that was killed.
		if err := admin.Exec("DROP DATABASE IF EXISTS " + cfg.Name + " WITH (FORCE)").Error; err != nil {
			t.Errorf("drop database %s: %v", cfg.Name, err)
		}
	})
	return conn, cfg
}

// env points a command at the test database.
func env(cfg config.DBConfig) []string {
	return append(os.Environ(),
		"DB_HOST="+cfg.Host, "DB_PORT="+cfg.Port, "DB_USER="+cfg.User,
		"DB_PASSWORD="+cfg.Password, "DB_NAME="+cfg.Name, "DB_SSLMODE="+cfg.SSLMode,
	)
}
