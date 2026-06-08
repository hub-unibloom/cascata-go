package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

type WebhookPayload struct {
	EventType string      `json:"event_type"`
	Table     string      `json:"table"`
	Schema    string      `json:"schema"`
	Record    interface{} `json:"record"`
	OldRecord interface{} `json:"old_record,omitempty"`
	Timestamp string      `json:"timestamp"`
}

type FilterRule struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// DispatchWebhook finds active webhooks for an event and enqueues them
func DispatchWebhook(ctx context.Context, projectSlug, tableName, eventType string, payloadData interface{}, projectSecret string) {
	rows, err := SystemPool.Query(ctx, 
		`SELECT target_url, secret_header, event_type, filters, fallback_url, retry_policy 
		 FROM system.webhooks 
		 WHERE project_slug = $1 
		 AND is_active = true 
		 AND (table_name = '*' OR table_name = $2)
		 AND (event_type = '*' OR event_type = $3)`,
		projectSlug, tableName, eventType)
	
	if err != nil {
		log.Printf("[WebhookService] Query Error: %v", err)
		return
	}
	defer rows.Close()

	fullPayload := WebhookPayload{
		EventType: eventType,
		Table:     tableName,
		Schema:    "public",
		Record:    payloadData,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	dispatchedCount := 0
	for rows.Next() {
		var targetURL, secretHeader, hookEventType, fallbackURL, retryPolicy string
		var filters []FilterRule
		
		var filtersRaw json.RawMessage
		if err := rows.Scan(&targetURL, &secretHeader, &hookEventType, &filtersRaw, &fallbackURL, &retryPolicy); err != nil {
			continue
		}
		json.Unmarshal(filtersRaw, &filters)

		if !matchesFilters(payloadData, filters) {
			continue
		}

		secret := secretHeader
		if secret == "" { secret = projectSecret }

		// Enqueue Job
		err := AddWebhookJob(ctx, map[string]interface{}{
			"targetUrl":   targetURL,
			"payload":     fullPayload,
			"secret":      secret,
			"eventType":   eventType,
			"tableName":   tableName,
			"fallbackUrl": fallbackURL,
			"retryPolicy": retryPolicy,
		})
		
		if err == nil {
			dispatchedCount++
		}
	}

	if dispatchedCount > 0 {
		log.Printf("[WebhookService] Dispatched %d events for %s (%s)", dispatchedCount, tableName, eventType)
	}
}

func matchesFilters(record interface{}, filters []FilterRule) bool {
	if len(filters) == 0 { return true }
	if record == nil { return false }

	recMap, ok := record.(map[string]interface{})
	if !ok { return false }

	for _, rule := range filters {
		valA, exists := recMap[rule.Field]
		if !exists { return false }
		
		valB := rule.Value
		strA := fmt.Sprintf("%v", valA)

		switch rule.Operator {
		case "eq": if strA != valB { return false }
		case "neq": if strA == valB { return false }
		case "contains": if !strings.Contains(strings.ToLower(strA), strings.ToLower(valB)) { return false }
		case "starts_with": if !strings.HasPrefix(strA, valB) { return false }
		// TODO: Implement numeric gt/lt logic if needed
		}
	}
	return true
}
