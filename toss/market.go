package toss

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Prices returns the API result for GET /api/v1/prices. symbols must contain
// one through 200 Toss symbols. The returned JSON is the response's result
// value, preserving Toss fields such as symbol, timestamp, lastPrice, and
// currency without imposing a lossy decimal representation.
func (client *Client) Prices(ctx context.Context, symbols []string) (json.RawMessage, error) {
	if len(symbols) < 1 || len(symbols) > 200 {
		return nil, fmt.Errorf("toss: symbols must contain 1..200 values")
	}
	for _, symbol := range symbols {
		if strings.TrimSpace(symbol) == "" || strings.Contains(symbol, ",") {
			return nil, fmt.Errorf("toss: symbol must be a non-empty comma-free value")
		}
	}
	return client.get(ctx, "/api/v1/prices", url.Values{"symbols": {strings.Join(symbols, ",")}})
}

// MarketCalendar returns the API result for GET /api/v1/market-calendar/{KR|US}.
// market must be KR or US; date, when non-empty, is sent as the date query field.
func (client *Client) MarketCalendar(ctx context.Context, market, date string) (json.RawMessage, error) {
	market = strings.ToUpper(market)
	if market != "KR" && market != "US" {
		return nil, fmt.Errorf("toss: market must be KR or US")
	}
	query := url.Values{}
	if date != "" {
		query.Set("date", date)
	}
	return client.get(ctx, "/api/v1/market-calendar/"+market, query)
}
