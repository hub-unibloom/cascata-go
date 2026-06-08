package services

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// dragonfly is the internal redis client
var dragonfly *redis.Client

func InitDragonfly(addr string) {
	dragonfly = redis.NewClient(&redis.Options{
		Addr: addr,
	})
	log.Println("[Dragonfly] Connected to cache layer.")
}

// GetDragonfly returns the global redis client (Exported for sibling packages)
func GetDragonfly() *redis.Client {
	return dragonfly
}

// --- INTERFACES & TYPES ---

type RateLimitRule struct {
	ID                string                 `json:"id"`
	ProjectSlug       string                 `json:"project_slug"`
	RoutePattern      string                 `json:"route_pattern"`
	Method            string                 `json:"method"`
	RateLimit         int                    `json:"rate_limit"`
	BurstLimit        int                    `json:"burst_limit"`
	WindowSeconds     int                    `json:"window_seconds"`
	RateLimitAnon     *int                   `json:"rate_limit_anon"`
	BurstLimitAnon    *int                   `json:"burst_limit_anon"`
	RateLimitAuth     *int                   `json:"rate_limit_auth"`
	BurstLimitAuth    *int                   `json:"burst_limit_auth"`
	WindowSecondsAnon *int                   `json:"window_seconds_anon"`
	WindowSecondsAuth *int                   `json:"window_seconds_auth"`
	MessageAnon       *string                `json:"message_anon"`
	MessageAuth       *string                `json:"message_auth"`
	IsCumulative      bool                   `json:"is_cumulative"`
	IsActive          bool                   `json:"is_active"`
	OperationWeights  map[string]int         `json:"operation_weights"`
	CrudLimits        *ResourceLimitConfig   `json:"crud_limits"`
	GroupLimits       map[string]interface{} `json:"group_limits"`
	TimeWindows       []TimeWindow           `json:"time_windows"`
}

type TimeWindow struct {
	Type          string `json:"type"`            // "minute", "hour", "day"
	WindowSeconds int    `json:"window_seconds"`
	Limit         int    `json:"limit"`
	Burst         int    `json:"burst"`
}

type ResourceLimitConfig struct {
	Anon *CrudConfig `json:"anon"`
	Auth *CrudConfig `json:"auth"`
}

type CrudConfig struct {
	Read   *int `json:"read"`
	Create *int `json:"create"`
	Update *int `json:"update"`
	Delete *int `json:"delete"`
}

type ApiKeyData struct {
	ID         string     `json:"id"`
	GroupID    *string    `json:"group_id"`
	RateLimit  *int       `json:"rate_limit"`
	BurstLimit *int       `json:"burst_limit"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	IsNerfed   bool       `json:"is_nerfed"`
}

type KeyGroupData struct {
	ID                  string      `json:"id"`
	Name                string      `json:"name"`
	RateLimit           int         `json:"rate_limit"`
	BurstLimit          int         `json:"burst_limit"`
	WindowSeconds       int         `json:"window_seconds"`
	RejectionMessage    string      `json:"rejection_message"`
	NerfConfig          *NerfConfig `json:"nerf_config"`
	CrudLimits          *CrudConfig `json:"crud_limits"`
	IsCumulativeDefault bool        `json:"is_cumulative_default"`
}

type NerfConfig struct {
	Enabled           bool   `json:"enabled"`
	StartDelaySeconds int    `json:"start_delay_seconds"`
	Mode              string `json:"mode"`
	SpeedPct          int    `json:"speed_pct"`
	StopAfterSeconds  int    `json:"stop_after_seconds"`
}

type RateCheckResult struct {
	Blocked       bool   `json:"blocked"`
	Limit         int    `json:"limit"`
	Remaining     int    `json:"remaining"`
	RetryAfter    int    `json:"retry_after"`
	CustomMessage string `json:"custom_message"`
}

type AuthSecurityConfig struct {
	MaxAttempts    int    `json:"max_attempts"`
	LockoutMinutes int    `json:"lockout_minutes"`
	Strategy       string `json:"strategy"`
	Disabled       bool   `json:"disabled"`
}

// --- STATE MANAGEMENT ---

var (
	rulesCache        sync.Map // [string][]RateLimitRule
	l1ProjectCache    sync.Map // [string]interface{}
	keysCache         sync.Map // [string]apiKeyCacheItem
	groupsCache       sync.Map // [string]groupCacheItem
	ipBlocklist       sync.Map // [string]bool
	adaptiveMultiplier float64 = 1.0
	lastMultiplierCheck time.Time
	isAdaptiveEnabled   bool = true
	lastBlocklistSync   time.Time
)

type apiKeyCacheItem struct {
	data     *ApiKeyData
	cachedAt time.Time
}

type groupCacheItem struct {
	data     *KeyGroupData
	cachedAt time.Time
}

// --- INITIALIZATION ---

func InitRateLimit() {
	if dragonfly == nil {
		return
	}

	ctx := context.Background()
	
	// Subscribe to Cache Invalidation
	pubsub := dragonfly.Subscribe(ctx, "sys:cache:invalidate", "cascata_cache_invalidate")
	go func() {
		defer pubsub.Close()
		for msg := range pubsub.Channel() {
			if msg.Channel == "sys:cache:invalidate" {
				if strings.HasPrefix(msg.Payload, "slug:") || strings.HasPrefix(msg.Payload, "domain:") {
					l1ProjectCache.Delete(msg.Payload)
				} else {
					l1ProjectCache.Delete("slug:" + msg.Payload)
					l1ProjectCache.Delete("domain:" + msg.Payload)
				}
			} else if msg.Channel == "cascata_cache_invalidate" {
				var payload struct {
					Table string `json:"table"`
				}
				if err := json.Unmarshal([]byte(msg.Payload), &payload); err == nil {
					// Invalidate semantic cache entries
					keys, _ := dragonfly.Keys(ctx, "qcache:"+payload.Table+":*").Result()
					if len(keys) > 0 {
						dragonfly.Del(ctx, keys...)
					}
				}
			}
		}
	}()

	log.Println("[RateLimit] Native Go Synchronizer Active.")
}

// --- STORAGE QUOTA ---

func ReserveStorage(ctx context.Context, projectSlug string, bytes int64, ttl time.Duration) string {
	if dragonfly == nil { return "" }
	id := generateUUID()
	key := fmt.Sprintf("storage:reserved:%s:%s", projectSlug, id)
	dragonfly.Set(ctx, key, bytes, ttl)
	return id
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func ReleaseStorage(ctx context.Context, projectSlug string, id string) {
	if dragonfly == nil { return }
	key := fmt.Sprintf("storage:reserved:%s:%s", projectSlug, id)
	dragonfly.Del(ctx, key)
}

func GetReservedStorage(ctx context.Context, projectSlug string) int64 {
	if dragonfly == nil { return 0 }
	keys, _ := dragonfly.Keys(ctx, fmt.Sprintf("storage:reserved:%s:*", projectSlug)).Result()
	if len(keys) == 0 { return 0 }
	
	count := int64(0)
	vals, _ := dragonfly.MGet(ctx, keys...).Result()
	for _, v := range vals {
		if v != nil {
			b, _ := strconv.ParseInt(v.(string), 10, 64)
			count += b
		}
	}
	return count
}

// --- IP BLOCKLIST & STRIKES ---

func RegisterStrike(ctx context.Context, ip string, reason string) {
	if dragonfly == nil { return }
	key := "abuser:strikes:" + ip
	strikes, _ := dragonfly.Incr(ctx, key).Result()
	if strikes == 1 {
		dragonfly.Expire(ctx, key, time.Hour)
	}

	if strikes >= 10 {
		dragonfly.SAdd(ctx, "sys:firewall:blocklist", ip)
		ipBlocklist.Store(ip, true)
		log.Printf("[EdgeDefense] IP %s blocked. Strikes: %d. Reason: %s", ip, strikes, reason)
	}
}

func IsIpBlocked(ctx context.Context, ip string) bool {
	if time.Since(lastBlocklistSync) > 10*time.Second {
		syncBlocklist(ctx)
	}
	_, blocked := ipBlocklist.Load(ip)
	return blocked
}

func syncBlocklist(ctx context.Context) {
	if dragonfly == nil { return }
	list, _ := dragonfly.SMembers(ctx, "sys:firewall:blocklist").Result()
	
	// Reset blocklist
	ipBlocklist.Range(func(key, value interface{}) bool {
		ipBlocklist.Delete(key)
		return true
	})
	
	for _, ip := range list {
		ipBlocklist.Store(ip, true)
	}
	lastBlocklistSync = time.Now()
}

// --- ADAPTIVE RATE LIMITING ---

func GetAdaptiveMultiplier(ctx context.Context) float64 {
	if !isAdaptiveEnabled { return 1.0 }
	if time.Since(lastMultiplierCheck) < 2*time.Second { return adaptiveMultiplier }
	
	lastMultiplierCheck = time.Now()
	if dragonfly == nil { return 1.0 }

	loadStr, _ := dragonfly.Get(ctx, "sys:health:cpu_load").Result()
	load, _ := strconv.ParseFloat(loadStr, 64)

	if load > 90 {
		adaptiveMultiplier = 0.5
	} else if load > 75 {
		adaptiveMultiplier = 0.8
	} else {
		adaptiveMultiplier = 1.0
	}
	return adaptiveMultiplier
}

// --- AUTH LOCKOUT ---

func CheckAuthLockout(ctx context.Context, slug, ip, identifier string, config *AuthSecurityConfig) (bool, string) {
	if dragonfly == nil || config == nil || config.Disabled { return false, "" }

	strategy := config.Strategy
	if strategy == "" { strategy = "hybrid" }
	maxAttempts := config.MaxAttempts
	if maxAttempts == 0 { maxAttempts = 5 }

	if strategy == "hybrid" || strategy == "ip" {
		ipKey := fmt.Sprintf("lockout:ip:%s:%s", slug, ip)
		strikes, _ := dragonfly.Get(ctx, ipKey).Int()
		if strikes >= (maxAttempts * 3) {
			return true, fmt.Sprintf("Too many failed attempts from your network. Locked for %d minutes.", config.LockoutMinutes)
		}
	}

	if identifier != "" && (strategy == "hybrid" || strategy == "identifier" || strategy == "email") {
		idKey := fmt.Sprintf("lockout:id:%s:%s", slug, identifier)
		strikes, _ := dragonfly.Get(ctx, idKey).Int()
		if strikes >= maxAttempts {
			return true, fmt.Sprintf("Too many failed attempts for this account. Locked for %d minutes.", config.LockoutMinutes)
		}
	}

	return false, ""
}

func RegisterAuthFailure(ctx context.Context, slug, ip, identifier string, config *AuthSecurityConfig) {
	if dragonfly == nil || config == nil || config.Disabled { return }

	strategy := config.Strategy
	if strategy == "" { strategy = "hybrid" }
	lockout := time.Duration(config.LockoutMinutes) * time.Minute
	if lockout == 0 { lockout = 15 * time.Minute }

	pipe := dragonfly.Pipeline()
	if strategy == "hybrid" || strategy == "ip" {
		key := fmt.Sprintf("lockout:ip:%s:%s", slug, ip)
		pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, lockout)
	}
	if identifier != "" && (strategy == "hybrid" || strategy == "identifier" || strategy == "email") {
		key := fmt.Sprintf("lockout:id:%s:%s", slug, identifier)
		pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, lockout)
	}
	pipe.Exec(ctx)
}

// --- THE CORE CHECK METHOD ---

func CheckRateLimit(ctx context.Context, pgPool *pgxpool.Pool, slug, resource, method, role, ip, token string) RateCheckResult {
	if dragonfly == nil { return RateCheckResult{Blocked: false} }

	// 1. Edge Defense
	if IsIpBlocked(ctx, ip) {
		return RateCheckResult{Blocked: true, RetryAfter: 3600, CustomMessage: "Firewall: Your IP is blacklisted."}
	}

	// 2. Adaptive Math
	adaptiveMult := GetAdaptiveMultiplier(ctx)

	// 3. Subject & Tier Identification
	subject := ip
	tier := "anon"
	var apiKey *ApiKeyData

	if strings.HasPrefix(token, "sk_") {
		apiKey = validateApiKey(ctx, SystemPool, token, slug)
		if apiKey != nil {
			tier = "custom_key"
			subject = apiKey.ID
		}
	} else if role == "authenticated" && token != "" {
		tier = "auth"
		// In a real scenario, we'd decode the JWT to get the sub. 
	}

	// 4. Rule Matching - Use SystemPool for system.rate_limits
	rules := getRules(ctx, SystemPool, slug)
	var matchedRule *RateLimitRule
	for _, r := range rules {
		if (r.Method == "ALL" || r.Method == method) && matchesPattern(r.RoutePattern, resource) {
			matchedRule = &r
			break
		}
	}

	// 5. Parameter Determination
	limit := 50
	burst := 50
	windowSecs := 1
	isCumulative := false
	var crudConfig *CrudConfig
	ruleID := "default"
	customMsg := "Rate limit exceeded"

	if matchedRule != nil {
		ruleID = matchedRule.ID
		windowSecs = matchedRule.WindowSeconds
		isCumulative = matchedRule.IsCumulative
		
		if tier == "custom_key" && apiKey.GroupID != nil {
			// Start with Key Group defaults
			group := getGroupData(ctx, SystemPool, *apiKey.GroupID)
			if group != nil {
				limit = group.RateLimit
				burst = group.BurstLimit
				windowSecs = group.WindowSeconds
				crudConfig = group.CrudLimits
				if group.RejectionMessage != "" { customMsg = group.RejectionMessage }

				// Check if there is a rule-specific override for this group in matchedRule.GroupLimits
				if matchedRule.GroupLimits != nil {
					if grpOverrideVal, ok := matchedRule.GroupLimits[*apiKey.GroupID]; ok {
						if grpOverride, ok := grpOverrideVal.(map[string]interface{}); ok {
							if rVal, ok := grpOverride["rate"]; ok {
								if rNum, ok := rVal.(float64); ok {
									limit = int(rNum)
								} else if rInt, ok := rVal.(int); ok {
									limit = rInt
								}
							}
							if bVal, ok := grpOverride["burst"]; ok {
								if bNum, ok := bVal.(float64); ok {
									burst = int(bNum)
								} else if bInt, ok := bVal.(int); ok {
									burst = bInt
								}
							}
							if crudVal, ok := grpOverride["crud"]; ok {
								if crudMap, ok := crudVal.(map[string]interface{}); ok {
									cc := &CrudConfig{}
									if rd, ok := crudMap["read"]; ok {
										if rdn, ok := rd.(float64); ok { val := int(rdn); cc.Read = &val }
									}
									if cr, ok := crudMap["create"]; ok {
										if crn, ok := cr.(float64); ok { val := int(crn); cc.Create = &val }
									}
									if up, ok := crudMap["update"]; ok {
										if upn, ok := up.(float64); ok { val := int(upn); cc.Update = &val }
									}
									if dl, ok := crudMap["delete"]; ok {
										if dln, ok := dl.(float64); ok { val := int(dln); cc.Delete = &val }
									}
									crudConfig = cc
								}
							}
						}
					}
				}

				if apiKey.IsNerfed {
					speed := float64(group.NerfConfig.SpeedPct) / 100.0
					limit = int(math.Max(1, float64(limit)*speed))
					burst = 0
				}
			}
		} else if tier == "auth" {
			if matchedRule.RateLimitAuth != nil { limit = *matchedRule.RateLimitAuth } else { limit = matchedRule.RateLimit * 2 }
			if matchedRule.BurstLimitAuth != nil { burst = *matchedRule.BurstLimitAuth } else { burst = matchedRule.BurstLimit * 2 }
			if matchedRule.WindowSecondsAuth != nil && *matchedRule.WindowSecondsAuth > 0 {
				windowSecs = *matchedRule.WindowSecondsAuth
			}
			if matchedRule.MessageAuth != nil && *matchedRule.MessageAuth != "" {
				customMsg = *matchedRule.MessageAuth
			}
			if matchedRule.CrudLimits != nil { crudConfig = matchedRule.CrudLimits.Auth }
		} else {
			if matchedRule.RateLimitAnon != nil { limit = *matchedRule.RateLimitAnon } else { limit = matchedRule.RateLimit }
			if matchedRule.BurstLimitAnon != nil { burst = *matchedRule.BurstLimitAnon } else { burst = matchedRule.BurstLimit }
			if matchedRule.WindowSecondsAnon != nil && *matchedRule.WindowSecondsAnon > 0 {
				windowSecs = *matchedRule.WindowSecondsAnon
			}
			if matchedRule.MessageAnon != nil && *matchedRule.MessageAnon != "" {
				customMsg = *matchedRule.MessageAnon
			}
			if matchedRule.CrudLimits != nil { crudConfig = matchedRule.CrudLimits.Anon }
		}
	}

	// 6. Operation Weight
	operation := getOperation(method)
	weight := 1
	if operation != "" && matchedRule != nil {
		if w, ok := matchedRule.OperationWeights[operation]; ok {
			weight = w
		} else {
			// Defaults
			weights := map[string]int{"read": 1, "create": 5, "update": 2, "delete": 3}
			weight = weights[operation]
		}
		
		// CRUD Overrides
		if crudConfig != nil {
			var specLimit *int
			switch operation {
				case "read": specLimit = crudConfig.Read
				case "create": specLimit = crudConfig.Create
				case "update": specLimit = crudConfig.Update
				case "delete": specLimit = crudConfig.Delete
			}
			if specLimit != nil {
				if *specLimit == -1 { return RateCheckResult{Blocked: false} }
				limit = *specLimit
				burst = limit / 2
				ruleID += ":" + operation
			}
		}
	}

	// 7. Multi-Window Check
	windows := []TimeWindow{{Type: "default", WindowSeconds: windowSecs, Limit: limit, Burst: burst}}
	if matchedRule != nil && len(matchedRule.TimeWindows) > 0 {
		windows = matchedRule.TimeWindows
	}

	type winRes struct {
		key      string
		limit    int
		burst    int
		secs     int
		useQuota bool
	}
	results := make([]winRes, 0, len(windows))

	for _, w := range windows {
		winLimit := int(float64(w.Limit) * adaptiveMult)
		winBurst := int(float64(w.Burst) * adaptiveMult)
		winKey := fmt.Sprintf("rate:%s:%s:%s:%s:%s", slug, tier, subject, ruleID, w.Type)
		
		useQuota := false
		if isCumulative && apiKey != nil && !apiKey.IsNerfed && subject != "anon" {
			balance := getQuotaBalance(ctx, SystemPool, slug, tier, subject, ruleID, w.Type)
			if balance >= int64(weight) { useQuota = true }
		}

		if !useQuota {
			current, _ := dragonfly.Get(ctx, winKey).Int()
			if current+weight > (winLimit + winBurst) {
				RegisterStrike(ctx, ip, "exceeding limits")
				ttl, _ := dragonfly.TTL(ctx, winKey).Result()
				return RateCheckResult{
					Blocked: true,
					Limit: winLimit,
					RetryAfter: int(ttl.Seconds()),
					CustomMessage: fmt.Sprintf("%s (%s limit)", customMsg, w.Type),
				}
			}
		}
		results = append(results, winRes{winKey, winLimit, winBurst, w.WindowSeconds, useQuota})
	}

	// 8. Commitment Phase
	for _, r := range results {
		if r.useQuota {
			updateQuotaBalance(ctx, SystemPool, slug, tier, subject, ruleID, "default", int64(-weight))
		} else {
			pipe := dragonfly.Pipeline()
			pipe.IncrBy(ctx, r.key, int64(weight))
			pipe.TTL(ctx, r.key)
			exec, _ := pipe.Exec(ctx)
			if len(exec) > 1 && exec[1].(*redis.DurationCmd).Val() < 0 {
				dragonfly.Expire(ctx, r.key, time.Duration(r.secs)*time.Second)
			}
		}
	}

	return RateCheckResult{Blocked: false, Limit: limit, Remaining: 1}
}

// --- HELPERS ---

func validateApiKey(ctx context.Context, pool *pgxpool.Pool, key, slug string) *ApiKeyData {
	// 1. L1 Cache Hit
	if val, ok := keysCache.Load(key); ok {
		item := val.(apiKeyCacheItem)
		if time.Since(item.cachedAt) < 30*time.Second { return item.data }
	}

	// 2. Database Lookup
	parts := strings.Split(key, "_")
	if len(parts) != 4 { return nil }
	idx := fmt.Sprintf("%s_%s_%s", parts[0], parts[1], parts[2])

	var hash, id string
	var groupID *string
	var rateLim, burstLim *int
	var scopes []string
	var expiresAt *time.Time
	var vaultItemID *string

	err := pool.QueryRow(ctx, 
		`SELECT id, group_id, rate_limit, burst_limit, scopes, expires_at, key_hash, vault_item_id 
		 FROM system.api_keys 
		 WHERE project_slug = $1 AND lookup_index = $2 AND is_active = true`,
		slug, idx).Scan(&id, &groupID, &rateLim, &burstLim, &scopes, &expiresAt, &hash, &vaultItemID)
	
	if err != nil { return nil }

	// 3. SECURE VALIDATION (Vault vs Bcrypt)
	if vaultItemID != nil && GlobalVaultSvc != nil {
		// --- GLORY PATH: Vault-based verification ---
		valid, err := GlobalVaultSvc.VerifySecret(ctx, slug, *vaultItemID, key)
		if err != nil || !valid {
			log.Printf("[RateLimit] Vault validation failed for key %s in project %s: %v", id, slug, err)
			return nil
		}
		log.Printf("[RateLimit] ✓ API Key %s validated via Vault (Project: %s)", id, slug)
	} else {
		// --- LEGACY PATH: Bcrypt-based verification ---
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(key)) != nil { return nil }
		log.Printf("[RateLimit] API Key %s validated via Legacy Bcrypt (Project: %s)", id, slug)
	}

	data := &ApiKeyData{ID: id, GroupID: groupID, RateLimit: rateLim, BurstLimit: burstLim, Scopes: scopes, ExpiresAt: expiresAt}
	
	// Nerf Logic
	if expiresAt != nil && time.Now().After(*expiresAt) {
		if groupID != nil {
			group := getGroupData(ctx, pool, *groupID)
			if group != nil && group.NerfConfig != nil && group.NerfConfig.Enabled {
				diff := time.Since(*expiresAt).Seconds()
				if diff > float64(group.NerfConfig.StartDelaySeconds) {
					if group.NerfConfig.StopAfterSeconds > -1 && diff > float64(group.NerfConfig.StartDelaySeconds+group.NerfConfig.StopAfterSeconds) {
						return nil // Fully expired
					}
					data.IsNerfed = true
				}
			} else { return nil }
		} else { return nil }
	}

	keysCache.Store(key, apiKeyCacheItem{data: data, cachedAt: time.Now()})
	return data
}

func getRules(ctx context.Context, pool *pgxpool.Pool, slug string) []RateLimitRule {
	if val, ok := rulesCache.Load(slug); ok {
		return val.([]RateLimitRule)
	}

	rows, err := pool.Query(ctx, `
		SELECT id, route_pattern, method, rate_limit, burst_limit, window_seconds,
		       rate_limit_anon, burst_limit_anon, rate_limit_auth, burst_limit_auth,
		       window_seconds_anon, window_seconds_auth, message_anon, message_auth,
		       crud_limits, group_limits, time_windows, operation_weights, is_cumulative
		FROM system.rate_limits
		WHERE project_slug = $1
	`, slug)
	if err != nil { return nil }
	defer rows.Close()

	var rules []RateLimitRule
	for rows.Next() {
		var r RateLimitRule
		var crudBytes, groupBytes, timeWindowsBytes, weightsBytes []byte
		err := rows.Scan(
			&r.ID, &r.RoutePattern, &r.Method, &r.RateLimit, &r.BurstLimit, &r.WindowSeconds,
			&r.RateLimitAnon, &r.BurstLimitAnon, &r.RateLimitAuth, &r.BurstLimitAuth,
			&r.WindowSecondsAnon, &r.WindowSecondsAuth, &r.MessageAnon, &r.MessageAuth,
			&crudBytes, &groupBytes, &timeWindowsBytes, &weightsBytes, &r.IsCumulative,
		)
		if err != nil {
			log.Printf("[RateLimit] Error scanning rate limit rule: %v", err)
			continue
		}

		if len(crudBytes) > 0 {
			json.Unmarshal(crudBytes, &r.CrudLimits)
		}
		if len(groupBytes) > 0 {
			json.Unmarshal(groupBytes, &r.GroupLimits)
		}
		if len(timeWindowsBytes) > 0 {
			json.Unmarshal(timeWindowsBytes, &r.TimeWindows)
		}
		if len(weightsBytes) > 0 {
			json.Unmarshal(weightsBytes, &r.OperationWeights)
		}

		rules = append(rules, r)
	}
	
	// Specificity Sort
	sort.Slice(rules, func(i, j int) bool {
		pA, pB := rules[i].RoutePattern, rules[j].RoutePattern
		if pA == "*" { return false }
		if pB == "*" { return true }
		return len(pA) > len(pB)
	})

	rulesCache.Store(slug, rules)
	return rules
}

func getGroupData(ctx context.Context, pool *pgxpool.Pool, id string) *KeyGroupData {
	if val, ok := groupsCache.Load(id); ok {
		item := val.(groupCacheItem)
		if time.Since(item.cachedAt) < time.Minute { return item.data }
	}
	
	var g KeyGroupData
	err := pool.QueryRow(ctx, "SELECT id, name, rate_limit, burst_limit, window_seconds, rejection_message, is_cumulative_default FROM system.api_key_groups WHERE id = $1", id).
		Scan(&g.ID, &g.Name, &g.RateLimit, &g.BurstLimit, &g.WindowSeconds, &g.RejectionMessage, &g.IsCumulativeDefault)
	
	if err != nil { return nil }
	groupsCache.Store(id, groupCacheItem{data: &g, cachedAt: time.Now()})
	return &g
}

func getQuotaBalance(ctx context.Context, pool *pgxpool.Pool, slug, tier, sub, rule, win string) int64 {
	var bal int64
	pool.QueryRow(ctx, "SELECT balance FROM system.quota_balances WHERE project_slug = $1 AND tier = $2 AND subject_id = $3 AND resource_id = $4 AND window_type = $5",
		slug, tier, sub, rule, win).Scan(&bal)
	return bal
}

func updateQuotaBalance(ctx context.Context, pool *pgxpool.Pool, slug, tier, sub, rule, win string, delta int64) {
	_, _ = pool.Exec(ctx, `
		INSERT INTO system.quota_balances (project_slug, tier, subject_id, resource_id, window_type, balance, last_reset)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (project_slug, tier, subject_id, resource_id, window_type)
		DO UPDATE SET balance = system.quota_balances.balance + $6, last_reset = NOW()`,
		slug, tier, sub, rule, win, delta)
}

func matchesPattern(pattern, resource string) bool {
	if pattern == "*" { return true }
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(resource, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == resource
}

func getOperation(method string) string {
	switch method {
	case "GET":
		return "read"
	case "POST":
		return "create"
	case "PUT", "PATCH":
		return "update"
	case "DELETE":
		return "delete"
	}
	return ""
}

// --- STORAGE QUOTA WRAPPERS (without context for easier use) ---

func GetProjectStorageUsage(projectSlug string) int64 {
	if dragonfly == nil {
		return -1
	}
	ctx := context.Background()
	key := fmt.Sprintf("storage:usage:%s", projectSlug)
	val, err := dragonfly.Get(ctx, key).Result()
	if err != nil || val == "" {
		return -1
	}
	usage, _ := strconv.ParseInt(val, 10, 64)
	return usage
}

func SetProjectStorageUsage(projectSlug string, usage int64, ttl time.Duration) {
	if dragonfly == nil {
		return
	}
	ctx := context.Background()
	key := fmt.Sprintf("storage:usage:%s", projectSlug)
	dragonfly.Set(ctx, key, usage, ttl)
}

func InvalidateProjectStorageUsage(projectSlug string) {
	if dragonfly == nil {
		return
	}
	ctx := context.Background()
	key := fmt.Sprintf("storage:usage:%s", projectSlug)
	dragonfly.Del(ctx, key)
}

// --- PANIC MODE (HARD SECURITY - Edge Defense) ---
// These functions check the panic state from Dragonfly (in-memory cache)
// BEFORE requests reach PostgreSQL, providing immediate lockdown capability

// CheckPanic returns true if the project is in panic mode (lockdown)
func CheckPanic(slug string) bool {
	if dragonfly == nil {
		return false
	}
	ctx := context.Background()
	val, err := dragonfly.Get(ctx, fmt.Sprintf("panic:%s", slug)).Result()
	if err != nil {
		return false
	}
	return val == "true"
}

// SetPanic enables or disables panic mode for a project
// adminIdentifier pode ser IP ou UserID do admin que ativou (para whitelisting)
func SetPanic(slug string, state bool, adminIdentifier string) error {
	if dragonfly == nil {
		return fmt.Errorf("dragonfly not connected")
	}
	ctx := context.Background()
	key := fmt.Sprintf("panic:%s", slug)
	adminKey := fmt.Sprintf("panic:admin:%s", slug)
	
	if state {
		// Ativa panic mode e salva identificador do admin (IP ou UserID)
		pipe := dragonfly.Pipeline()
		pipe.Set(ctx, key, "true", 0)
		if adminIdentifier != "" {
			pipe.Set(ctx, adminKey, adminIdentifier, 0)
		}
		_, err := pipe.Exec(ctx)
		return err
	}
	
	// Desativa panic mode - limpa tudo
	pipe := dragonfly.Pipeline()
	pipe.Del(ctx, key)
	pipe.Del(ctx, adminKey)
	_, err := pipe.Exec(ctx)
	return err
}

// GetPanicAdmin retorna o identificador (IP ou UserID) do admin que ativou o panic
func GetPanicAdmin(slug string) string {
	if dragonfly == nil {
		return ""
	}
	ctx := context.Background()
	val, err := dragonfly.Get(ctx, fmt.Sprintf("panic:admin:%s", slug)).Result()
	if err != nil {
		return ""
	}
	return val
}

// IsAdminWhitelisted verifica se o identificador (IP ou UserID) é o admin que ativou o panic
func IsAdminWhitelisted(slug, identifier string) bool {
	if dragonfly == nil || identifier == "" {
		return false
	}
	admin := GetPanicAdmin(slug)
	return admin != "" && admin == identifier
}

// CheckUserNeutralized returns true if a specific user has been neutralized (banned) during panic
func CheckUserNeutralized(slug, userId string) bool {
	if dragonfly == nil {
		return false
	}
	ctx := context.Background()
	key := fmt.Sprintf("panic:user:%s:%s", slug, userId)
	val, err := dragonfly.Get(ctx, key).Result()
	if err != nil {
		return false
	}
	return val == "true"
}

// SetUserNeutralized neutralizes (bans) or un-neutralizes a specific user
func SetUserNeutralized(slug, userId string, active bool, ttlSeconds int) error {
	if dragonfly == nil {
		return fmt.Errorf("dragonfly not connected")
	}
	ctx := context.Background()
	key := fmt.Sprintf("panic:user:%s:%s", slug, userId)
	if active {
		ttl := time.Duration(ttlSeconds) * time.Second
		return dragonfly.Set(ctx, key, "true", ttl).Err()
	}
	return dragonfly.Del(ctx, key).Err()
}

// TrackGlobalRPS increments the RPS counter for a project (for dashboard display)
func TrackGlobalRPS(slug string) {
	if dragonfly == nil {
		return
	}
	ctx := context.Background()
	key := fmt.Sprintf("rps:%s", slug)
	pipe := dragonfly.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 2*time.Second)
	pipe.Exec(ctx)
}

// GetCurrentRPS returns the current requests per second for a project
func GetCurrentRPS(slug string) int64 {
	if dragonfly == nil {
		return 0
	}
	ctx := context.Background()
	key := fmt.Sprintf("rps:%s", slug)
	val, err := dragonfly.Get(ctx, key).Result()
	if err != nil {
		return 0
	}
	rps, _ := strconv.ParseInt(val, 10, 64)
	return rps
}
