// Command ikigai-cli is the Ikigai DB command line, for pipelines (FR-16.1).
//
// A binary of its own rather than a mode of the application's, for one reason:
// this one must run where there is no display. A binary that draws links
// against the platform's window system whether it opens a window or not, so a
// container image would have to carry those libraries to run "ikigai query" —
// and a test here holds this binary to having none of them.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/ikigai-db/ikigai-db/internal/cli"

	// Drivers register themselves on import (REQ-DB-1). The same list the
	// application carries, because a pipeline reaching fewer sources than the
	// window would be a surprise nobody could see the reason for.
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/cassandra"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/clickhouse"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/cockroach"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/dynamodb"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/firebird"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/kafka"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/libsql"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/mongo"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/mysql"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/oracle"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/redis"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlite"
	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlserver"
)

func main() {
	// Interrupted means stopped, not killed: a load in a transaction rolls it
	// back, and a half-written file is removed rather than left looking whole.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
