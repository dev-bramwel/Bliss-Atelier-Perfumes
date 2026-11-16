package telemetry

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbPoolCollector struct {
	pool *pgxpool.Pool
}

func (c *dbPoolCollector) Collect() {
	if c.pool == nil {
		return
	}
	stat := c.pool.Stat()
	DBPoolConnections.Set(float64(stat.AcquiredConns()), "acquired")
	DBPoolConnections.Set(float64(stat.IdleConns()), "idle")
	DBPoolConnections.Set(float64(stat.TotalConns()), "total")
	DBPoolMaxConnections.Set(float64(stat.MaxConns()))
}

// RegisterPoolStats registers a dynamic collector for PostgreSQL connection pool stats.
func RegisterPoolStats(pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	DefaultRegistry.RegisterCollector(&dbPoolCollector{pool: pool})
}
