package tools

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
)

// referenceSuffix splits a reference like "PO-0007" into ("PO-", 7, 4).
var referenceSuffix = regexp.MustCompile(`^(.*?)(\d+)$`)

// referenceHolder decodes just the reference field from any order-like list
// endpoint. Purchase, sales, return and build orders all carry one.
type referenceHolder struct {
	Reference string `json:"reference"`
}

// nextReference picks the next unused reference for an order type.
//
// InvenTree validates `reference` against a per-model pattern setting --
// PURCHASEORDER_REFERENCE_PATTERN, SALESORDER_REFERENCE_PATTERN,
// RETURNORDER_REFERENCE_PATTERN, BUILDORDER_REFERENCE_PATTERN, defaulting to
// "PO-{ref:04d}", "SO-{ref:04d}", "RO-{ref:04d}" and "BO-{ref:04d}" -- so a
// free-form string such as a vendor's own order number is rejected outright.
// Rather than require the caller to know the pattern, infer it from existing
// records and continue the series.
//
// listPath is the collection endpoint to learn from (e.g. "/api/order/po/").
// fallbackPrefix is used only when no existing record has a numeric suffix to
// learn from, which in practice means the very first order of that type.
//
// This races under concurrent creates: two callers reading the same highest
// reference will generate the same next one and the loser gets a uniqueness
// error. Pass an explicit reference for batch work.
func nextReference(c *client.Client, listPath, fallbackPrefix string) (string, error) {
	var resp client.PaginatedResponse[referenceHolder]
	if err := c.Get(listPath+"?limit=500&format=json", &resp); err != nil {
		return "", fmt.Errorf("reading existing records from %s: %w", listPath, err)
	}

	prefix, width, highest := fallbackPrefix, 4, 0
	for _, rec := range resp.Results {
		m := referenceSuffix.FindStringSubmatch(rec.Reference)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		if n > highest {
			prefix, width, highest = m[1], len(m[2]), n
		}
	}
	return fmt.Sprintf("%s%0*d", prefix, width, highest+1), nil
}
