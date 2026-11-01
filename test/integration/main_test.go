//go:build integration

// Package integration runs the oracle against a real Postgres: the quote store, the whole
// sync → history → strategy pipeline, and the compiled binary of every command.
//
// It uses its own Postgres, the postgres-test service of docker-compose.yml, and is excluded from
// `go test ./...`. `make test-integration` starts that database and runs the tests, locally and in CI.
//
// Before the tests, the schema is seeded once into a template database. Every test then gets its
// own database, cloned from the template or empty, and drops it at the end, so tests run in
// parallel without seeing each other's data. B3 and Yahoo are still faked: the real services are
// slow, rate limited and change every day.
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
	"oracle/internal/quote"
	"oracle/pkg/db"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	// server is the test Postgres; admin is connected to its default database, only to create and drop the others.
	server config.DBConfig
	admin  *gorm.DB
	// template is the database with the seeded schema that test databases are cloned from.
	template string
	// binary is the oracle command, built once for the CLI tests.
	binary string
)

// testServer reads the TEST_DB_* variables, never DB_*, so the tests cannot reach the database you use day to day.
// The defaults match the postgres-test service.
func testServer() config.DBConfig {
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	return config.DBConfig{
		Host:     get("TEST_DB_HOST", "localhost"),
		Port:     get("TEST_DB_PORT", "5433"),
		User:     get("TEST_DB_USER", "oracle_test"),
		Password: get("TEST_DB_PASSWORD", "oracle_test"),
		Name:     get("TEST_DB_NAME", "oracle_test"),
		SSLMode:  get("TEST_DB_SSLMODE", "disable"),
	}
}

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	log.SetOutput(io.Discard) // the sync logs every file it skips

	server = testServer()
	var err error
	if admin, err = connect(server); err != nil {
		fmt.Fprintf(os.Stderr, "integration: cannot reach the test Postgres at %s:%s (%v)\nstart it with `make test-db`\n", server.Host, server.Port, err)
		return 1
	}

	template = uniqueName("oracle_tpl_")
	if err := seed(template); err != nil {
		fmt.Fprintf(os.Stderr, "integration: seed: %v\n", err)
		return 1
	}
	defer admin.Exec("DROP DATABASE IF EXISTS " + template + " WITH (FORCE)")

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

// seed creates the template database with the schema the commands migrate to.
func seed(name string) error {
	if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
		return err
	}
	cfg := server
	cfg.Name = name
	conn, err := connect(cfg)
	if err != nil {
		return err
	}
	defer closeDB(conn) // a template cannot be cloned while connected
	return quote.NewPostgres(conn).Migrate(context.Background())
}

// connect opens a database the way the commands do, and checks it answers.
func connect(cfg config.DBConfig) (*gorm.DB, error) {
	conn, err := db.Connect(cfg)
	if err != nil {
		return nil, err
	}
	conn = conn.Session(&gorm.Session{Logger: logger.Discard})
	sqlDB, err := conn.DB()
	if err != nil {
		return nil, err
	}
	return conn, sqlDB.Ping()
}

func closeDB(conn *gorm.DB) {
	if sqlDB, err := conn.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func uniqueName(prefix string) string {
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	return prefix + hex.EncodeToString(suffix)
}

// newDatabase gives the test a database with the schema already in place, and drops it at the end.
func newDatabase(t *testing.T) (*gorm.DB, config.DBConfig) {
	t.Helper()
	return createDatabase(t, " TEMPLATE "+template)
}

// emptyDatabase gives the test a database without tables, as before the first sync.
func emptyDatabase(t *testing.T) (*gorm.DB, config.DBConfig) {
	t.Helper()
	return createDatabase(t, "")
}

func createDatabase(t *testing.T, options string) (*gorm.DB, config.DBConfig) {
	t.Helper()
	cfg := server
	cfg.Name = uniqueName("oracle_it_")
	if err := admin.Exec("CREATE DATABASE " + cfg.Name + options).Error; err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		// FORCE also ends connections left open by a command that was killed.
		if err := admin.Exec("DROP DATABASE IF EXISTS " + cfg.Name + " WITH (FORCE)").Error; err != nil {
			t.Errorf("drop database %s: %v", cfg.Name, err)
		}
	})
	conn, err := connect(cfg)
	if err != nil {
		t.Fatalf("connect to %s: %v", cfg.Name, err)
	}
	t.Cleanup(func() { closeDB(conn) })
	return conn, cfg
}

// insert stores the bars directly, as a sync would have.
func insert(t *testing.T, conn *gorm.DB, series ...[]quote.Quote) {
	t.Helper()
	for _, bars := range series {
		if err := conn.CreateInBatches(bars, 500).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// env points a command at the test database.
func env(cfg config.DBConfig) []string {
	return append(os.Environ(),
		"DB_HOST="+cfg.Host, "DB_PORT="+cfg.Port, "DB_USER="+cfg.User,
		"DB_PASSWORD="+cfg.Password, "DB_NAME="+cfg.Name, "DB_SSLMODE="+cfg.SSLMode,
	)
}
