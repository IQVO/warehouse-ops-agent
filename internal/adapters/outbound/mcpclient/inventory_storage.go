package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// InventoryStorage implements ports.InventoryStorageClient by calling
// inventory-storage's published check_availability and get_bin_occupancy
// MCP tools.
type InventoryStorage struct {
	session *Session
}

// NewInventoryStorage builds an InventoryStorage client for the given
// connection config.
func NewInventoryStorage(cfg Config) *InventoryStorage {
	return &InventoryStorage{session: NewNamedSession("inventory-storage", cfg)}
}

func (c *InventoryStorage) CheckAvailability(ctx context.Context, sku string) (ports.Availability, error) {
	var out ports.Availability
	err := c.session.callTool(ctx, "check_availability", map[string]any{"sku": sku}, &out)
	return out, err
}

func (c *InventoryStorage) GetBinOccupancy(ctx context.Context, binId string) (ports.BinOccupancy, error) {
	var out ports.BinOccupancy
	err := c.session.callTool(ctx, "get_bin_occupancy", map[string]any{"binId": binId}, &out)
	return out, err
}
