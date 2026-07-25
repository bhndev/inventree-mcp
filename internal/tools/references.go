package tools

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
)

// referenceSuffix splits a reference like "PO-0007" into ("PO-", 7, 4).
var referenceSuffix = regexp.MustCompile(`^(.*?)(\d+)$`)

// referencePattern parses an InvenTree reference pattern such as
// "PO-{ref:04d}" or "RMA-{ref:04d}" into its prefix, zero-padding width and
// trailing suffix. The width group is optional: "{ref}" is a valid pattern.
var referencePattern = regexp.MustCompile(`^(.*?)\{ref(?::0*(\d+)d)?\}(.*)$`)

// referenceHolder decodes just the reference field from any order-like list
// endpoint. Purchase, sales, return and build orders all carry one.
type referenceHolder struct {
	Reference string `json:"reference"`
}

// nextReference picks the next unused reference for an order type.
//
// InvenTree validates `reference` against a per-model pattern setting, and
// those patterns are user-configurable: an instance may use "RMA-{ref:04d}"
// for return orders rather than the "RO-" default. Guessing the prefix
// therefore fails on any customised instance, so the pattern is read from the
// settings API and used as the authority.
//
// settingKey is the global setting holding the pattern, e.g.
// "PURCHASEORDER_REFERENCE_PATTERN". listPath is the collection endpoint used
// to find the highest reference already issued. fallbackPrefix applies only
// when the setting cannot be read at all.
//
// This races under concurrent creates: two callers reading the same highest
// reference will generate the same next one and the loser gets a uniqueness
// error. Pass an explicit reference for batch work.
func nextReference(c *client.Client, listPath, settingKey, fallbackPrefix string) (string, error) {
	prefix, width, suffix := fallbackPrefix, 4, ""

	// The configured pattern is authoritative. If it cannot be read, fall back
	// to inferring the shape from existing records below rather than failing:
	// a working guess beats refusing to create the order.
	var setting struct {
		Value string `json:"value"`
	}
	patternKnown := false
	if err := c.Get("/api/settings/global/"+settingKey+"/?format=json", &setting); err == nil {
		if m := referencePattern.FindStringSubmatch(setting.Value); m != nil {
			prefix, suffix, patternKnown = m[1], m[3], true
			if m[2] != "" {
				if n, err := strconv.Atoi(m[2]); err == nil && n > 0 {
					width = n
				}
			}
		}
	}

	var resp client.PaginatedResponse[referenceHolder]
	if err := c.Get(listPath+"?limit=500&format=json", &resp); err != nil {
		return "", fmt.Errorf("reading existing records from %s: %w", listPath, err)
	}

	highest := 0
	for _, rec := range resp.Results {
		m := referenceSuffix.FindStringSubmatch(rec.Reference)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= highest {
			continue
		}
		highest = n
		// Without a known pattern, infer the shape from the highest existing
		// reference instead. With one, the pattern wins: a legacy record using
		// an older prefix must not drag the new reference back to it.
		if !patternKnown {
			prefix, width = m[1], len(m[2])
		}
	}
	return fmt.Sprintf("%s%0*d%s", prefix, width, highest+1, suffix), nil
}
