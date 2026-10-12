package selvedge

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestConformancePostgres(t *testing.T) {
	required := os.Getenv("LOOM_MCP_REQUIRE_DOCKER_TESTS") == "1"
	if !required && os.Getenv("LOOM_MCP_RUN_DOCKER_TESTS") != "1" {
		t.Skip("set LOOM_MCP_RUN_DOCKER_TESTS=1 for PostgreSQL conformance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image: "postgres:16-alpine", Env: map[string]string{"POSTGRES_USER": "test", "POSTGRES_PASSWORD": "test", "POSTGRES_DB": "postgres"}, ExposedPorts: []string{"5432/tcp"}, WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(time.Minute),
	}, Started: true})
	if err != nil {
		if required {
			require.NoError(t, err, "PostgreSQL testcontainer required")
		}
		t.Skipf("PostgreSQL testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() {
		require.NoError(t, container.Terminate(context.Background()))
	})
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	address := net.JoinHostPort(host, port.Port())
	admin, err := sql.Open("pgx", fmt.Sprintf("postgres://test:test@%s/postgres?sslmode=disable", address))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, admin.Close())
	})
	enginetest.RunConformance(t, conformanceFactory(func(t *testing.T) string {
		name := "scenario_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+name)
		require.NoError(t, err)
		return fmt.Sprintf("postgres://test:test@%s/%s?sslmode=disable", address, name)
	}))
}
