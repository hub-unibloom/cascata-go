package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cascata-backend/internal/services"
)

// EdgeRateLimiterMiddleware bloqueia ataques ANTES de qualquer conexão com PostgreSQL
// Usa apenas Dragonfly (Redis) - nenhuma consulta ao banco de dados
// Deve ser executado ANTES do ProjectResolver
func EdgeRateLimiterMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. IP Blacklist Check (Dragonfly - ultra rápido)
		ip := getClientIP(r)
		if services.IsIpBlocked(r.Context(), ip) {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"IP Blacklisted","blocked":true}`))
			return
		}

		// 2. API Key Extraction para rate limit por token (sem validar no banco ainda)
		apiKey := r.Header.Get("apikey")
		if apiKey == "" {
			apiKey = r.URL.Query().Get("apikey")
		}

		// 3. Hard Rate Limit por IP (DDoS Protection)
		// Usa apenas Dragonfly, nunca toca no PostgreSQL
		ipKey := fmt.Sprintf("edge:ratelimit:ip:%s", ip)
		
		// Verifica contador no Dragonfly
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Millisecond)
		defer cancel()
		
		// Hard limit: 100 req/min por IP antes de resolver projeto
		count, _ := services.GetDragonfly().Incr(ctx, ipKey).Result()
		if count == 1 {
			services.GetDragonfly().Expire(ctx, ipKey, time.Minute)
		}
		
		if count > 100 {
			// IP excedeu limite hard - bloqueia imediatamente
			services.RegisterStrike(ctx, ip, "edge rate limit exceeded")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"Rate limit exceeded","retry_after":60}`))
			return
		}

		// 4. Token-based hard limit (se tem API key)
		if apiKey != "" && strings.HasPrefix(apiKey, "sk_") {
			tokenKey := fmt.Sprintf("edge:ratelimit:token:%s", apiKey)
			tokenCount, _ := services.GetDragonfly().Incr(ctx, tokenKey).Result()
			if tokenCount == 1 {
				services.GetDragonfly().Expire(ctx, tokenKey, time.Minute)
			}
			
			// Hard limit: 1000 req/min por token
			if tokenCount > 1000 {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Token rate limit exceeded","retry_after":60}`))
				return
			}
		}

		// Passou pelos checks de edge - continua para ProjectResolver
		next.ServeHTTP(w, r)
	})
}

