package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PurchaseOrder status codes used by InvenTree.
const (
	POStatusPending   = 10
	POStatusPlaced    = 20
	POStatusOnHold    = 25
	POStatusComplete  = 30
	POStatusCancelled = 40
)

// poSummary holds only the purchase-order fields this package needs
// internally. Everything returned to the caller is decoded into
// map[string]any instead, so InvenTree can change or add response fields --
// or return a money value as a number rather than a string -- without
// breaking these tools.
type poSummary struct {
	PK         int    `json:"pk"`
	Reference  string `json:"reference"`
	Status     int    `json:"status"`
	StatusText string `json:"status_text"`
}

// -- List Purchase Orders --

type ListPurchaseOrdersInput struct {
	Supplier    int    `json:"supplier,omitempty" jsonschema:"Filter by supplier company ID. 0 or omit for all."`
	Status      int    `json:"status,omitempty" jsonschema:"Filter by status code: 10 pending, 20 placed, 25 on hold, 30 complete, 40 cancelled. 0 or omit for all."`
	Outstanding *bool  `json:"outstanding,omitempty" jsonschema:"Filter to orders that are still outstanding"`
	Search      string `json:"search,omitempty" jsonschema:"Search text matched against reference and description"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterListPurchaseOrders(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_purchase_orders",
		Description: "List purchase orders, optionally filtered by supplier, status, or outstanding state. Use this to find an order ID before adding lines or receiving stock.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListPurchaseOrdersInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/order/po/?limit=%d&format=json", limit)
		if input.Supplier != 0 {
			path += fmt.Sprintf("&supplier=%d", input.Supplier)
		}
		if input.Status != 0 {
			path += fmt.Sprintf("&status=%d", input.Status)
		}
		if input.Outstanding != nil {
			path += fmt.Sprintf("&outstanding=%t", *input.Outstanding)
		}
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}

		var resp client.PaginatedResponse[map[string]any]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing purchase orders: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Purchase Order --

type GetPurchaseOrderInput struct {
	ID int `json:"id" jsonschema:"The purchase order ID (pk)"`
}

func RegisterGetPurchaseOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "get_purchase_order",
		Description: "Get a purchase order with all of its line items, including ordered and received quantities per line. Use this to see what is still outstanding before receiving a shipment.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetPurchaseOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var order map[string]any
		if err := c.Get(fmt.Sprintf("/api/order/po/%d/?format=json", input.ID), &order); err != nil {
			return errResult(fmt.Errorf("getting purchase order %d: %w", input.ID, err)), nil, nil
		}

		var lines client.PaginatedResponse[map[string]any]
		linePath := fmt.Sprintf("/api/order/po-line/?order=%d&limit=500&format=json", input.ID)
		if err := c.Get(linePath, &lines); err != nil {
			return errResult(fmt.Errorf("getting lines for order %d: %w", input.ID, err)), nil, nil
		}

		return jsonResult(map[string]any{
			"order": order,
			"lines": lines.Results,
		})
	})
}

// -- Create Purchase Order --

type CreatePurchaseOrderInput struct {
	Supplier          int    `json:"supplier" jsonschema:"Supplier company ID (required)"`
	SupplierReference string `json:"supplier_reference,omitempty" jsonschema:"The vendor's own order number, e.g. 'SO 100601233'. Put it here, not in reference."`
	Description       string `json:"description,omitempty" jsonschema:"Short description of the order"`
	Reference         string `json:"reference,omitempty" jsonschema:"Internal order reference. Omit to auto-generate the next in sequence, which is almost always what you want."`
	TargetDate        string `json:"target_date,omitempty" jsonschema:"Expected delivery date, YYYY-MM-DD"`
	Notes             string `json:"notes,omitempty" jsonschema:"Free-text notes, e.g. shipping and tax totals"`
}

func RegisterCreatePurchaseOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_purchase_order",
		Description: "Create a purchase order for a supplier. The order is created in PENDING status with no lines; " +
			"add lines with add_purchase_order_line, then issue_purchase_order before stock can be received. " +
			"Leave `reference` empty so it is auto-generated -- InvenTree enforces a reference pattern and rejects " +
			"free-form vendor strings. The vendor's own order number goes in supplier_reference.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreatePurchaseOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Supplier == 0 {
			return errResult(fmt.Errorf("supplier is required")), nil, nil
		}

		reference := input.Reference
		if reference == "" {
			generated, err := nextReference(c, "/api/order/po/", "PURCHASEORDER_REFERENCE_PATTERN", "PO-")
			if err != nil {
				return errResult(err), nil, nil
			}
			reference = generated
		}

		description := input.Description
		if description == "" {
			description = input.SupplierReference
		}

		payload := map[string]any{
			"supplier":    input.Supplier,
			"reference":   reference,
			"description": description,
		}
		if input.SupplierReference != "" {
			payload["supplier_reference"] = input.SupplierReference
		}
		if input.TargetDate != "" {
			payload["target_date"] = input.TargetDate
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var created map[string]any
		if err := c.Post("/api/order/po/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating purchase order (reference %q): %w", reference, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Add Purchase Order Line --

type AddPurchaseOrderLineInput struct {
	Order        int     `json:"order" jsonschema:"Purchase order ID (required)"`
	SupplierPart int     `json:"supplier_part" jsonschema:"SUPPLIER part ID, not the internal part ID (required). Find it with search_supplier_parts."`
	Quantity     float64 `json:"quantity" jsonschema:"Quantity ordered (required)"`
	UnitPrice    string  `json:"unit_price,omitempty" jsonschema:"Unit price as a decimal string, e.g. '141.82'. Omit if pricing is not yet known."`
	Currency     string  `json:"currency,omitempty" jsonschema:"Currency code for unit_price, e.g. USD. Defaults to USD when a price is given."`
	Reference    string  `json:"reference,omitempty" jsonschema:"Line reference"`
	Notes        string  `json:"notes,omitempty" jsonschema:"Line notes, e.g. backorder or substitution detail"`
	Destination  int     `json:"destination,omitempty" jsonschema:"Stock location ID where received stock should land. 0 or omit for none."`
	TargetDate   string  `json:"target_date,omitempty" jsonschema:"Expected date for this line, YYYY-MM-DD"`
}

func RegisterAddPurchaseOrderLine(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "add_purchase_order_line",
		Description: "Add a line item to a purchase order. The line references a SUPPLIER part (vendor SKU), not an internal part -- " +
			"create one with create_supplier_part first if the vendor has never supplied this part. " +
			"Unit price is per unit; InvenTree computes the extended total.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AddPurchaseOrderLineInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Order == 0 || input.SupplierPart == 0 {
			return errResult(fmt.Errorf("order and supplier_part are required")), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than zero")), nil, nil
		}

		payload := map[string]any{
			"order":    input.Order,
			"part":     input.SupplierPart,
			"quantity": input.Quantity,
		}
		if input.UnitPrice != "" {
			currency := input.Currency
			if currency == "" {
				currency = "USD"
			}
			payload["purchase_price"] = input.UnitPrice
			payload["purchase_price_currency"] = currency
		}
		if input.Reference != "" {
			payload["reference"] = input.Reference
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}
		if input.Destination != 0 {
			payload["destination"] = input.Destination
		}
		if input.TargetDate != "" {
			payload["target_date"] = input.TargetDate
		}

		var created map[string]any
		if err := c.Post("/api/order/po-line/", payload, &created); err != nil {
			return errResult(fmt.Errorf("adding line to order %d: %w", input.Order, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Issue Purchase Order --

type IssuePurchaseOrderInput struct {
	ID int `json:"id" jsonschema:"The purchase order ID (pk) to issue"`
}

func RegisterIssuePurchaseOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "issue_purchase_order",
		Description: "Issue (place) a pending purchase order, moving it from PENDING to PLACED. " +
			"Stock cannot be received against an order until it has been issued, so call this once the order " +
			"has actually been sent to the vendor.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input IssuePurchaseOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var result any
		path := fmt.Sprintf("/api/order/po/%d/issue/", input.ID)
		if err := c.Post(path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Errorf("issuing purchase order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Purchase order %d issued and is now PLACED. Stock can be received against it.", input.ID))
	})
}

// -- Receive Purchase Order --

// ReceiveItem is one line being received in a shipment.
type ReceiveItem struct {
	LineItem  int     `json:"line_item" jsonschema:"Purchase order LINE ITEM ID (required). Get it from get_purchase_order."`
	Quantity  float64 `json:"quantity" jsonschema:"Quantity received in this shipment (required). May be a partial delivery."`
	Location  int     `json:"location,omitempty" jsonschema:"Stock location for this line. 0 or omit to use the order-level location."`
	BatchCode string  `json:"batch_code,omitempty" jsonschema:"Batch code for the received stock"`
	Status    int     `json:"status,omitempty" jsonschema:"Stock status code: 10 OK (default), 50 attention needed, 55 damaged, 65 rejected, 75 quarantined."`
}

type ReceivePurchaseOrderInput struct {
	Order    int           `json:"order" jsonschema:"Purchase order ID (required). Must be in PLACED status."`
	Location int           `json:"location,omitempty" jsonschema:"Default stock location for all received items. Individual items may override it."`
	Items    []ReceiveItem `json:"items" jsonschema:"Line items being received (required). Partial receipts are supported: send only what arrived."`
}

func RegisterReceivePurchaseOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "receive_purchase_order",
		Description: "Receive stock against a placed purchase order, creating stock items and advancing the order's received quantities. " +
			"This is the correct way to bring ordered stock into inventory -- it keeps on-order quantities accurate, " +
			"unlike creating stock items directly with add_stock. " +
			"Supports partial deliveries: send only the lines and quantities that actually arrived, and call again for later shipments. " +
			"The order must be in PLACED status; issue it first with issue_purchase_order.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ReceivePurchaseOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Order == 0 {
			return errResult(fmt.Errorf("order is required")), nil, nil
		}
		if len(input.Items) == 0 {
			return errResult(fmt.Errorf("at least one item is required")), nil, nil
		}

		// Fail early with a clear message rather than a bare 400 from the API.
		var order poSummary
		if err := c.Get(fmt.Sprintf("/api/order/po/%d/?format=json", input.Order), &order); err != nil {
			return errResult(fmt.Errorf("getting purchase order %d: %w", input.Order, err)), nil, nil
		}
		if order.Status != POStatusPlaced {
			return errResult(fmt.Errorf(
				"purchase order %d is %s (status %d), not PLACED -- stock can only be received against a placed order; call issue_purchase_order first",
				input.Order, order.StatusText, order.Status)), nil, nil
		}

		items := make([]map[string]any, 0, len(input.Items))
		for i, it := range input.Items {
			if it.LineItem == 0 {
				return errResult(fmt.Errorf("items[%d]: line_item is required", i)), nil, nil
			}
			if it.Quantity <= 0 {
				return errResult(fmt.Errorf("items[%d]: quantity must be greater than zero", i)), nil, nil
			}

			entry := map[string]any{
				"line_item": it.LineItem,
				"quantity":  it.Quantity,
			}
			status := it.Status
			if status == 0 {
				status = 10 // OK
			}
			entry["status"] = status

			switch {
			case it.Location != 0:
				entry["location"] = it.Location
			case input.Location != 0:
				entry["location"] = input.Location
			}
			if it.BatchCode != "" {
				entry["batch_code"] = it.BatchCode
			}
			items = append(items, entry)
		}

		payload := map[string]any{"items": items}
		if input.Location != 0 {
			payload["location"] = input.Location
		}

		// The receive endpoint returns an ARRAY of created stock items, while
		// most InvenTree endpoints return an object. Decode into `any` so
		// either shape works.
		var result any
		path := fmt.Sprintf("/api/order/po/%d/receive/", input.Order)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("receiving against order %d: %w", input.Order, err)), nil, nil
		}
		return textResult(fmt.Sprintf(
			"Received %d line item(s) against purchase order %s. Call get_purchase_order to see updated received quantities.",
			len(items), order.Reference))
	})
}
