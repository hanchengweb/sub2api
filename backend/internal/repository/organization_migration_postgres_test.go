//go:build unit

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Uses only the dedicated loopback test cluster; never a business DSN.
// Start PostgreSQL on 55439 with user windhub_migration_test, then set
// WINDHUB_PG199_TEST=1. Each invocation creates and drops its own database.
func TestOrganizationMigrationPostgres(t *testing.T) {
	if os.Getenv("WINDHUB_PG199_TEST") != "1" {
		t.Skip("requires isolated PostgreSQL on 127.0.0.1:55439")
	}
	const baseDSN = "host=127.0.0.1 port=55439 user=windhub_migration_test sslmode=disable connect_timeout=5 dbname="
	admin, err := sql.Open("postgres", baseDSN+"postgres")
	require.NoError(t, err)
	defer admin.Close()
	name := fmt.Sprintf("windhub_migration_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	defer func() { _, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)"); require.NoError(t, err) }()
	db, err := sql.Open("postgres", baseDSN+name)
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const migration = "199_organization_service_identity.sql"
	old := fstest.MapFS{}
	entries, err := migrations.FS.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < migration {
			data, err := migrations.FS.ReadFile(entry.Name())
			require.NoError(t, err)
			old[entry.Name()] = &fstest.MapFile{Data: data}
		}
	}
	require.NoError(t, applyMigrationsFS(ctx, db, old))
	_, err = db.Exec(`INSERT INTO users(email,password_hash,role,balance,status,deleted_at)
	 SELECT 'legacy-'||n||'@example.invalid','preserved-hash',
	 CASE WHEN n=1 THEN 'admin' ELSE 'user' END,12.34567890,
	 CASE WHEN n=2 THEN 'disabled' ELSE 'active' END,
	 CASE WHEN n=3 THEN now() ELSE NULL END FROM generate_series(1,10000) n;
	 INSERT INTO api_keys(user_id,key,name) SELECT id,'synthetic-migration-key','legacy' FROM users WHERE email='legacy-1@example.invalid'`)
	require.NoError(t, err)
	snapshot := func() string {
		var value string
		require.NoError(t, db.QueryRow(`SELECT md5(string_agg(row_to_json(t)::text, '' ORDER BY id)) FROM
		 (SELECT id,email,password_hash,role,balance,status,deleted_at FROM users) t`).Scan(&value))
		return value
	}
	before := snapshot()
	data, err := migrations.FS.ReadFile(migration)
	require.NoError(t, err)
	only := fstest.MapFS{migration: &fstest.MapFile{Data: data}}
	assertUnapplied := func() {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name='users' AND column_name='account_type'`).Scan(&n))
		require.Zero(t, n)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE filename=$1`, migration).Scan(&n))
		require.Zero(t, n)
		require.Equal(t, before, snapshot())
	}
	// A failure after all DDL must undo columns, constraints, index and receipt.
	broken := fstest.MapFS{migration: &fstest.MapFile{Data: []byte(string(data) + "\nSELECT 1/0;")}}
	require.ErrorContains(t, applyMigrationsFS(ctx, db, broken), "division by zero")
	assertUnapplied()
	// Existing readers prevent ACCESS EXCLUSIVE: time out instead of hanging.
	reader, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = reader.Exec("SELECT id FROM users LIMIT 1")
	require.NoError(t, err)
	started := time.Now()
	err = applyMigrationsFS(ctx, db, only)
	require.NoError(t, reader.Rollback())
	require.ErrorContains(t, err, "lock timeout")
	require.Less(t, time.Since(started), 10*time.Second)
	assertUnapplied()
	// Concurrent startup and subsequent replay must apply exactly once.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	started = time.Now()
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = applyMigrationsFS(ctx, db, only) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	t.Logf("upgrade plus concurrent startup: %s (10000 synthetic users)", time.Since(started))
	require.NoError(t, applyMigrationsFS(ctx, db, only))
	require.Equal(t, before, snapshot())
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM users WHERE account_type='personal' AND organization_issuer='' AND organization_id='' AND organization_environment=''`).Scan(&n))
	require.Equal(t, 10000, n)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.key='synthetic-migration-key' AND u.email='legacy-1@example.invalid'`).Scan(&n))
	require.Equal(t, 1, n)
	_, err = db.Exec(`INSERT INTO users(email,password_hash) VALUES('old-writer@example.invalid','old-hash'); UPDATE users SET balance=balance-1 WHERE email='legacy-1@example.invalid'`)
	require.NoError(t, err)
	insert := func(email, org, env, role string) error {
		_, err := db.Exec(`INSERT INTO users(email,password_hash,account_type,organization_issuer,organization_id,organization_environment,role) VALUES($1,'unusable','organization_service','wemoreai',$2,$3,$4)`, email, org, env, role)
		return err
	}
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = insert(fmt.Sprintf("service-%d@example.invalid", i), "org-a", "test", "user")
		}(i)
	}
	wg.Wait()
	require.True(t, (errs[0] == nil) != (errs[1] == nil), "exactly one concurrent identity creation must succeed")
	require.NoError(t, insert("org-b@example.invalid", "org-b", "test", "user"))
	require.NoError(t, insert("production@example.invalid", "org-a", "production", "user"))
	require.Error(t, insert("invalid@example.invalid", "", "test", "user"))
	require.Error(t, insert("admin@example.invalid", "org-c", "test", "admin"))
	_, err = db.Exec(`UPDATE users SET deleted_at=now() WHERE account_type='organization_service' AND organization_id='org-a' AND organization_environment='test'`)
	require.NoError(t, err)
	require.Error(t, insert("reassign@example.invalid", "org-a", "test", "user"))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE filename=$1`, migration).Scan(&n))
	require.Equal(t, 1, n)
}
