package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Return order status codes used by InvenTree (order/status_codes.py).
const (
	ROStatusPending    = 10
	ROStatusInProgress = 20
	ROStatusOnHold     = 25
	ROStatusComplete   = 30
	ROStatusCancelled  = 40
)

// Return order line outcome codes. These record what happened to a returned
// item, and are set per line rather than on the order.
const (
	ROOutcomePending = 10
	ROOutcomeReturn  = 20
	ROOutcomeRepair  = 30
	ROOutcomeReplace = 40
	ROOutcomeRefund  = 50
	ROOutcomeReject  = 60
)

// roSummary holds only the return-order fields this package needs internally.
type roSummary struct {
	PK         int    `json:"pk"`
	Reference  string `json:"reference"`
	Status     int    `json:"status"`
	StatusText string `json:"status_text"`
}

// -- List Return Orders --

type ListReturnOrdersInput struct {
	Customer    int    `json:"customer,omitempty" jsonschema:"Filter by customer company ID. 0 or omit for all."`
	Status      int    `json:"status,omitempty" jsonschema:"Filter by status code: 10 pending, 20 in progress, 25 on hold, 30 complete, 40 cancelled. 0 or omit for all."`
	Outstanding *bool  `json:"outstanding,omitempty" jsonschema:"Filter to orders that are still outstanding"`
	Search      string `json:"search,omitempty" jsonschema:"Search text matched against reference and description"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterListReturnOrders(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_return_orders",
		Description: "List return orders (RMAs -- goods coming back from a customer), optionally filtered by customer, status, or outstanding state.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListReturnOrdersInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/order/ro/?limit=%d&format=json", limit)
		if input.Customer != 0 {
			path += fmt.Sprintf("&customer=%d", input.Customer)
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
			return errResult(fmt.Errorf("listing return orders: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Return Order --

type GetReturnOrderInput struct {
	ID int `json:"id" jsonschema:"The return order ID (pk)"`
}

func RegisterGetReturnOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_return_order",
		Description: "Get a return order with its line items. Each line references the specific stock item being returned, " +
			"along with its outcome (return, repair, replace, refund, reject) and whether it has been received back yet. " +
			"Call this to get the line_item IDs needed by receive_return_order.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetReturnOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var order map[string]any
		if err := c.Get(fmt.Sprintf("/api/order/ro/%d/?format=json", input.ID), &order); err != nil {
			return errResult(fmt.Errorf("getting return order %d: %w", input.ID, err)), nil, nil
		}

		var lines client.PaginatedResponse[map[string]any]
		linePath := fmt.Sprintf("/api/order/ro-line/?order=%d&limit=500&format=json", input.ID)
		if err := c.Get(linePath, &lines); err != nil {
			return errResult(fmt.Errorf("getting lines for return order %d: %w", input.ID, err)), nil, nil
		}

		return jsonResult(map[string]any{
			"order": order,
			"lines": lines.Results,
		})
	})
}

// -- Create Return Order --

type CreateReturnOrderInput struct {
	Customer          int    `json:"customer" jsonschema:"Customer company ID returning the goods (required)"`
	CustomerReference string `json:"customer_reference,omitempty" jsonschema:"The customer's own RMA or reference number"`
	Description       string `json:"description,omitempty" jsonschema:"Short description of the return"`
	Reference         string `json:"reference,omitempty" jsonschema:"Internal order reference. Omit to auto-generate the next in sequence, which is almost always what you want."`
	TargetDate        string `json:"target_date,omitempty" jsonschema:"Target completion date, YYYY-MM-DD"`
	Notes             string `json:"notes,omitempty" jsonschema:"Free-text notes"`
}

func RegisterCreateReturnOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_return_order",
		Description: "Create a return order (RMA) for goods coming back from a customer. The order is created in PENDING status with no lines; " +
			"add lines with add_return_order_line, issue it with issue_return_order, then receive the goods with receive_return_order. " +
			"Leave `reference` empty so it is auto-generated -- InvenTree enforces a reference pattern and rejects free-form strings.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateReturnOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Customer == 0 {
			return errResult(fmt.Errorf("customer is required")), nil, nil
		}

		reference := input.Reference
		if reference == "" {
			generated, err := nextReference(c, "/api/order/ro/", "RO-")
			if err != nil {
				return errResult(err), nil, nil
			}
			reference = generated
		}

		description := input.Description
		if description == "" {
			description = input.CustomerReference
		}

		payload := map[string]any{
			"customer":    input.Customer,
			"reference":   reference,
			"description": description,
		}
		if input.CustomerReference != "" {
			payload["customer_reference"] = input.CustomerReference
		}
		if input.TargetDate != "" {
			payload["target_date"] = input.TargetDate
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var created map[string]any
		if err := c.Post("/api/order/ro/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating return order (reference %q): %w", reference, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Add Return Order Line --

type AddReturnOrderLineInput struct {
	Order     int     `json:"order" jsonschema:"Return order ID (required)"`
	StockItem int     `json:"stock_item" jsonschema:"STOCK ITEM ID being returned (required). A return order line references a specific serialised or batched stock item that was sold, not a part."`
	Quantity  float64 `json:"quantity,omitempty" jsonschema:"Quantity being returned. Defaults to 1; serialised items are always 1."`
	Outcome   int     `json:"outcome,omitempty" jsonschema:"What will happen to the item: 10 pending (default), 20 return to stock, 30 repair, 40 replace, 50 refund, 60 reject."`
	Price     string  `json:"price,omitempty" jsonschema:"Price associated with this return line as a decimal string, e.g. '19.99'"`
	Currency  string  `json:"currency,omitempty" jsonschema:"Currency code for price, e.g. USD. Defaults to USD when a price is given."`
	Reference string  `json:"reference,omitempty" jsonschema:"Line reference"`
	Notes     string  `json:"notes,omitempty" jsonschema:"Line notes describing the fault or reason for return"`
}

func RegisterAddReturnOrderLine(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "add_return_order_line",
		Description: "Add a line to a return order. " +
			"Unlike purchase and sales order lines, a return order line references a specific STOCK ITEM -- the actual unit being sent back -- " +
			"so the item must already exist in InvenTree as stock that was sold to this customer. " +
			"The outcome field records what will be done with it once received.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AddReturnOrderLineInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Order == 0 || input.StockItem == 0 {
			return errResult(fmt.Errorf("order and stock_item are required")), nil, nil
		}

		payload := map[string]any{
			"order": input.Order,
			"item":  input.StockItem,
		}
		if input.Quantity > 0 {
			payload["quantity"] = input.Quantity
		}
		if input.Outcome != 0 {
			payload["outcome"] = input.Outcome
		}
		if input.Price != "" {
			currency := input.Currency
			if currency == "" {
				currency = "USD"
			}
			payload["price"] = input.Price
			payload["price_currency"] = currency
		}
		if input.Reference != "" {
			payload["reference"] = input.Reference
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var created map[string]any
		if err := c.Post("/api/order/ro-line/", payload, &created); err != nil {
			return errResult(fmt.Errorf("adding line to return order %d: %w", input.Order, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Issue Return Order --

type IssueReturnOrderInput struct {
	ID int `json:"id" jsonschema:"The return order ID (pk) to issue"`
}

func RegisterIssueReturnOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "issue_return_order",
		Description: "Issue a pending return order, moving it from PENDING to IN PROGRESS. " +
			"Goods cannot be received against a return order until it has been issued.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input IssueReturnOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var result any
		path := fmt.Sprintf("/api/order/ro/%d/issue/", input.ID)
		if err := c.Post(path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Errorf("issuing return order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Return order %d issued and is now IN PROGRESS. Goods can be received against it.", input.ID))
	})
}

// -- Receive Return Order --

// ReturnReceiveItem is one line being received back into stock.
type ReturnReceiveItem struct {
	LineItem int `json:"line_item" jsonschema:"Return order LINE ITEM ID (required). Get it from get_return_order."`
}

type ReceiveReturnOrderInput struct {
	Order    int                 `json:"order" jsonschema:"Return order ID (required). Must be IN PROGRESS."`
	Location int                 `json:"location" jsonschema:"Stock location ID the returned goods are received into (required)"`
	Items    []ReturnReceiveItem `json:"items" jsonschema:"Line items being received (required). Partial receipts are supported: send only what arrived."`
	Notes    string              `json:"notes,omitempty" jsonschema:"Notes recorded against the receipt"`
}

func RegisterReceiveReturnOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "receive_return_order",
		Description: "Receive returned goods against an issued return order, bringing the stock items back into inventory at the given location. " +
			"Supports partial receipts: send only the lines that actually arrived and call again for the rest. " +
			"The order must be IN PROGRESS; issue it first with issue_return_order.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ReceiveReturnOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Order == 0 {
			return errResult(fmt.Errorf("order is required")), nil, nil
		}
		if input.Location == 0 {
			return errResult(fmt.Errorf("location is required: returned goods must be received into a stock location")), nil, nil
		}
		if len(input.Items) == 0 {
			return errResult(fmt.Errorf("at least one item is required")), nil, nil
		}

		// Fail early with a clear message rather than a bare 400 from the API.
		var order roSummary
		if err := c.Get(fmt.Sprintf("/api/order/ro/%d/?format=json", input.Order), &order); err != nil {
			return errResult(fmt.Errorf("getting return order %d: %w", input.Order, err)), nil, nil
		}
		if order.Status != ROStatusInProgress {
			return errResult(fmt.Errorf(
				"return order %s is %s (status %d), not IN PROGRESS -- goods can only be received against an issued order; call issue_return_order first",
				order.Reference, order.StatusText, order.Status)), nil, nil
		}

		items := make([]map[string]any, 0, len(input.Items))
		for i, it := range input.Items {
			if it.LineItem == 0 {
				return errResult(fmt.Errorf("items[%d]: line_item is required", i)), nil, nil
			}
			items = append(items, map[string]any{"item": it.LineItem})
		}

		payload := map[string]any{
			"items":    items,
			"location": input.Location,
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var result any
		path := fmt.Sprintf("/api/order/ro/%d/receive/", input.Order)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("receiving against return order %d: %w", input.Order, err)), nil, nil
		}
		return textResult(fmt.Sprintf(
			"Received %d line item(s) against return order %s. Call get_return_order to see updated received dates.",
			len(items), order.Reference))
	})
}

// -- Complete Return Order --

type CompleteReturnOrderInput struct {
	ID int `json:"id" jsonschema:"The return order ID (pk) to complete"`
}

func RegisterCompleteReturnOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "complete_return_order",
		Description: "Complete a return order, moving it to COMPLETE. Receive the returned goods first with receive_return_order.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CompleteReturnOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var result any
		path := fmt.Sprintf("/api/order/ro/%d/complete/", input.ID)
		if err := c.Post(path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Errorf("completing return order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Return order %d completed.", input.ID))
	})
}

// -- Cancel Return Order --

type CancelReturnOrderInput struct {
	ID int `json:"id" jsonschema:"The return order ID (pk) to cancel"`
}

func RegisterCancelReturnOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "cancel_return_order",
		Description: "Cancel a return order, moving it to CANCELLED.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CancelReturnOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var order roSummary
		if err := c.Get(fmt.Sprintf("/api/order/ro/%d/?format=json", input.ID), &order); err != nil {
			return errResult(fmt.Errorf("getting return order %d: %w", input.ID, err)), nil, nil
		}
		switch order.Status {
		case ROStatusComplete, ROStatusCancelled:
			return errResult(fmt.Errorf("return order %s is already %s (status %d), so it cannot be cancelled",
				order.Reference, order.StatusText, order.Status)), nil, nil
		}

		var result any
		path := fmt.Sprintf("/api/order/ro/%d/cancel/", input.ID)
		if err := c.Post(path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Errorf("cancelling return order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Return order %s cancelled.", order.Reference))
	})
}
