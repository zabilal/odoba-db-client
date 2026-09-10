package grid

import (
	"context"
	"strconv"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// SyntheticFetcher generates rows deterministically, mirroring the shape of
// the seeded spike table (internal/ui/grid/testdata/seed.sql).
//
// It exists so the grid benchmarks run in CI without a database (NFR-Q3). The
// mix matters: a grid that is fast over ten integer columns proves nothing.
// Long text, NULLs, JSON, timestamps and numerics are what cost time to format
// and measure.
type SyntheticFetcher struct {
	Rows int64

	// Latency, when non-zero, is slept before returning a page, to model a
	// real round trip and prove the UI stays responsive during a fetch.
	Latency time.Duration

	cols []model.ColumnDef
}

var _ Fetcher = (*SyntheticFetcher)(nil)

// NewSyntheticFetcher builds a generator over n rows.
func NewSyntheticFetcher(n int64) *SyntheticFetcher {
	str := func(name string, class model.TypeClass, native string, nullable bool) model.ColumnDef {
		return model.ColumnDef{
			Name: name,
			Type: model.DataType{Class: class, Native: native, Nullable: nullable, Length: -1},
		}
	}
	return &SyntheticFetcher{
		Rows: n,
		cols: []model.ColumnDef{
			str("id", model.TypeInteger, "int8", false),
			str("sku", model.TypeString, "text", false),
			str("customer", model.TypeString, "text", false),
			str("email", model.TypeString, "text", true),
			str("status", model.TypeString, "text", false),
			str("quantity", model.TypeInteger, "int4", false),
			str("unit_price", model.TypeDecimal, "numeric", false),
			str("total", model.TypeDecimal, "numeric", false),
			str("is_priority", model.TypeBool, "bool", false),
			str("notes", model.TypeString, "text", true),
			str("metadata", model.TypeJSON, "jsonb", true),
			str("placed_at", model.TypeTimestamp, "timestamptz", false),
			str("shipped_at", model.TypeTimestamp, "timestamptz", true),
		},
	}
}

func (s *SyntheticFetcher) Columns() []model.ColumnDef { return s.cols }

func (s *SyntheticFetcher) Count(context.Context) (int64, error) { return s.Rows, nil }

var (
	synthNames    = []string{"Ada Lovelace", "Grace Hopper", "Alan Turing", "Barbara Liskov", "Edsger Dijkstra", "Katherine Johnson", "Donald Knuth", "Radia Perlman"}
	synthStatuses = []string{"pending", "paid", "shipped", "delivered", "refunded", "cancelled"}
	synthChannels = []string{"web", "ios", "android", "pos"}
	synthLongNote = "lorem ipsum dolor sit amet, consectetur adipiscing elit. " +
		"lorem ipsum dolor sit amet, consectetur adipiscing elit. " +
		"lorem ipsum dolor sit amet, consectetur adipiscing elit. "
	synthEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Fetch returns a page of generated rows.
func (s *SyntheticFetcher) Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error) {
	if s.Latency > 0 {
		select {
		case <-time.After(s.Latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if offset >= s.Rows {
		return nil, nil
	}
	if offset+limit > s.Rows {
		limit = s.Rows - offset
	}

	out := make([]model.Row, 0, limit)
	for k := int64(0); k < limit; k++ {
		i := offset + k + 1

		var email any
		if i%7 != 0 {
			email = "user" + strconv.FormatInt(i, 10) + "@example.com"
		}

		var notes any
		switch {
		case i%13 == 0:
			notes = synthLongNote // worst case for text measurement
		case i%3 == 0:
			notes = nil
		default:
			notes = "note " + strconv.FormatInt(i, 10)
		}

		var meta any
		if i%5 != 0 {
			meta = model.JSON(`{"channel":"` + synthChannels[i%4] +
				`","retries":` + strconv.FormatInt(i%4, 10) +
				`,"region":"emea"}`)
		}

		var shipped any
		if i%4 != 0 {
			shipped = synthEpoch.Add(time.Duration(i%2000000)*time.Minute + 48*time.Hour)
		}

		qty := 1 + i%40
		price := float64(i%50000) / 100.0

		out = append(out, model.Row{
			i,
			"SKU-" + pad6(i%99991),
			synthNames[i%8] + " " + strconv.FormatInt(i%4177, 10),
			email,
			synthStatuses[i%6],
			qty,
			model.Decimal(strconv.FormatFloat(price, 'f', 2, 64)),
			model.Decimal(strconv.FormatFloat(float64(qty)*price, 'f', 2, 64)),
			i%11 == 0,
			notes,
			meta,
			synthEpoch.Add(time.Duration(i%2000000) * time.Minute),
			shipped,
		})
	}
	return out, nil
}

func pad6(v int64) string {
	s := strconv.FormatInt(v, 10)
	if len(s) >= 6 {
		return s
	}
	return "000000"[:6-len(s)] + s
}
