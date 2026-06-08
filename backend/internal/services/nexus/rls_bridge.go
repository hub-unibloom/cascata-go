package nexus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ============================================================================
// RLS BRIDGE — Ponte de Segurança em Nível de Linha (Row-Level Security)
// ============================================================================
// Garante que todas as consultas SQL originadas de automações (Data Nodes)
// respeitem as políticas de RLS do PostgreSQL correspondentes à identidade
// que disparou o evento (via JWT Claims).
// ============================================================================

type RLSBridge struct {
	pool *pgxpool.Pool
}

// NewRLSBridge cria uma nova instância do RLS Bridge.
func NewRLSBridge(pool *pgxpool.Pool) *RLSBridge {
	return &RLSBridge{pool: pool}
}

// ExecRLS executa uma consulta no banco de dados respeitando o RLS.
// Inicia uma transação, configura o ROLE e JWT claims localmente, executa a query e faz o commit.
func (b *RLSBridge) ExecRLS(
	ctx context.Context, 
	secCtx *SecurityContext, 
	query string, 
	args ...interface{},
) ([]map[string]interface{}, error) {
	
	// Inicia a transação. O contexto da transação garantirá que as variáveis
	// locais SET LOCAL e set_config(..., true) sejam revertidas ao fim.
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("rls_bridge: falha ao iniciar transação: %w", err)
	}
	defer tx.Rollback(ctx) // Rollback seguro (no-op se já houve commit)

	// 1. Configurar o Role (auth_source -> role)
	role := secCtx.AuthSource
	if role == "" || role == "anon" || role == "webhook" {
		role = "anon"
	} else if role == "service_role" || role == "management" {
		// O service_role geralmente tem bypass RLS (bypassrls no PostgreSQL).
		role = "service_role"
	} else {
		// Fallback seguro: se não é explícito, tratamos como authenticated
		role = "authenticated"
	}

	// Prevenção contra injeção de SQL em SET ROLE
	if strings.ContainsAny(role, "';\x00\n\r") {
		return nil, fmt.Errorf("rls_bridge: role inválido")
	}

	_, err = tx.Exec(ctx, fmt.Sprintf("SET LOCAL ROLE %s;", role))
	if err != nil {
		return nil, fmt.Errorf("rls_bridge: falha ao definir role: %w", err)
	}

	// 2. Configurar os Claims (request.jwt.claims) para auth.uid() e políticas RLS
	if secCtx.UserUUID != "" {
		claims := map[string]interface{}{
			"sub":  secCtx.UserUUID,
			"role": role,
		}
		claimsJSON, _ := json.Marshal(claims)
		// is_local = true garante que o config dura apenas na transação
		_, err = tx.Exec(ctx, "SELECT set_config('request.jwt.claims', $1, true);", string(claimsJSON))
		if err != nil {
			return nil, fmt.Errorf("rls_bridge: falha ao definir jwt claims: %w", err)
		}
	} else {
		// Limpa claims antigos se for anon, para evitar vazamento de estado da sessão do pool
		_, err = tx.Exec(ctx, "SELECT set_config('request.jwt.claims', '', true);")
		if err != nil {
			return nil, fmt.Errorf("rls_bridge: falha ao limpar jwt claims: %w", err)
		}
	}

	// 3. Executar a consulta SQL real originada pelo Node
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("rls_bridge: falha na execução da query: %w", err)
	}
	defer rows.Close()

	// 4. Mapear resultados (dinâmico)
	var results []map[string]interface{}
	fieldDescriptions := rows.FieldDescriptions()

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("rls_bridge: erro ao ler valores da linha: %w", err)
		}

		rowMap := make(map[string]interface{})
		for i, fd := range fieldDescriptions {
			rowMap[string(fd.Name)] = values[i]
		}
		results = append(results, rowMap)
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("rls_bridge: erro iterando resultados: %w", rows.Err())
	}

	// 5. Commit seguro
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("rls_bridge: falha no commit: %w", err)
	}

	return results, nil
}
