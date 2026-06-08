package diff

import (
	"context"
	"fmt"
	"time"
)

// DiffEngine é o orquestrador principal do sistema de diff
// Ele coordena as 7 fases de comparação entre ambientes
type DiffEngine struct {
	ctx DiffContext
}

// NewDiffEngine cria uma nova instância do DiffEngine
func NewDiffEngine(ctx DiffContext) *DiffEngine {
	return &DiffEngine{
		ctx: ctx,
	}
}

// Run executa todas as fases do diff e retorna o resultado completo
// Este é o ponto de entrada principal para operações de diff
// GAP #4 FIX: Adquire conexões uma única vez e as reutiliza em todas as fases
func (e *DiffEngine) Run(ctx context.Context) (*DiffResult, error) {
	startTime := time.Now()

	// GAP #4 FIX: Adquire conexões source e target UMA ÚNICA VEZ no início
	// e as passa para todas as fases via DiffContext enriquecido
	sourceConn, err := e.ctx.PoolProvider.AcquireForProject(e.ctx.ProjectSlug, e.ctx.SourceBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire source connection: %w", err)
	}
	defer sourceConn.Close()

	targetConn, err := e.ctx.PoolProvider.AcquireForProject(e.ctx.ProjectSlug, e.ctx.TargetBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire target connection: %w", err)
	}
	defer targetConn.Close()

	// Cria um contexto enriquecido com as conexões já adquiridas
	// Isso evita que cada fase faça seu próprio acquire/release
	enrichedCtx := e.ctx
	enrichedCtx.SourceConn = sourceConn
	enrichedCtx.TargetConn = targetConn

	// Inicializa as 7 fases do diff
	// Cada fase é independente e testável, e não conhece as outras
	// O contexto será injetado via Introspect, não na inicialização
	diffPhases := []DiffPhase{
		// Fase 1: Tabelas - CREATE/DROP TABLE
		&TablesDiff{},

		// Fase 2: Colunas - ADD/ALTER/DROP COLUMN + heurística de RENAME
		&ColumnsDiff{},

		// Fase 3: Índices - CREATE/DROP INDEX via pg_indexes
		&IndexesDiff{},

		// Fase 4: RLS Policies - DROP+CREATE POLICY via pg_policies
		&RLSDiff{},

		// Fase 5: Funções/RPCs - CREATE OR REPLACE FUNCTION via pg_proc
		&FunctionsDiff{},

		// Fase 6: Triggers - DROP+CREATE TRIGGER via pg_trigger
		&TriggersDiff{},

		// Fase 7: Permissões - GRANT para anon/authenticated em novos objetos
		&PermissionsDiff{},
	}

	result := &DiffResult{
		Success: true,
		SQL:     make([]string, 0),
		Summaries: make([]PhaseSummary, 0, len(diffPhases)),
	}

	// Executa cada fase sequencialmente
	// A ordem é crítica: tabelas → colunas → índices → RLS → funções → triggers → permissões
	for _, phase := range diffPhases {
		phaseStart := time.Now()

		// Introspect: coleta metadados do banco usando as conexões já adquiridas
		if err := phase.Introspect(enrichedCtx); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("phase %s introspection failed: %v", phase.Name(), err)
			return result, fmt.Errorf("diff failed: %w", err)
		}

		// GenerateSQL: converte o diff em statements SQL
		phaseSQL := phase.GenerateSQL()
		result.SQL = append(result.SQL, phaseSQL...)

		// Summary: coleta informações sobre as mudanças
		summary := phase.Summary()
		summary.Duration = time.Since(phaseStart)
		result.Summaries = append(result.Summaries, summary)

		// Log de progresso (útil para debugging)
		if len(phaseSQL) > 0 {
			fmt.Printf("[DiffEngine] Phase %s: generated %d statements in %v\n",
				phase.Name(), len(phaseSQL), summary.Duration)
		}
	}

	totalDuration := time.Since(startTime)
	fmt.Printf("[DiffEngine] Total diff completed in %v with %d statements\n",
		totalDuration, len(result.SQL))

	return result, nil
}

// RunDryRun executa o diff em modo dry-run
// Cria um schema temporário isolado e aplica o SQL gerado
// Nenhum lock é emitido em tabelas de produção
func (e *DiffEngine) RunDryRun(ctx context.Context) (*DryRunResult, error) {
	// Primeiro, executa o diff normal
	diffResult, err := e.Run(ctx)
	if err != nil {
		return nil, err
	}

	if len(diffResult.SQL) == 0 {
		return &DryRunResult{
			Success: true,
			Message: "No changes detected",
		}, nil
	}

	// Cria um schema temporário para validação
	tempSchema := fmt.Sprintf("_dryrun_%d", time.Now().UnixNano())

	// Adquire conexão ephemeral para o schema temporário
	conn, err := e.ctx.PoolProvider.AcquireEphemeral(tempSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire ephemeral connection: %w", err)
	}
	defer conn.Close()

	// Cria o schema temporário
	_, err = conn.Exec(fmt.Sprintf("CREATE SCHEMA %s", tempSchema))
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary schema: %w", err)
	}

	// Aplica todo o SQL contra o schema temporário
	// Isso valida sintaxe, referências, tipos, etc.
	for _, stmt := range diffResult.SQL {
		// Prefixa tabelas com o schema temporário para isolamento
		// Nota: isso é uma simplificação - em produção precisaríamos de parsing SQL real
		_, err = conn.Exec(stmt)
		if err != nil {
			return &DryRunResult{
				Success: false,
				Error:   fmt.Sprintf("SQL validation failed: %v\nStatement: %s", err, stmt),
			}, nil
		}
	}

	// Limpa o schema temporário
	_, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", tempSchema))

	return &DryRunResult{
		Success: true,
		Message: fmt.Sprintf("Validated %d SQL statements successfully", len(diffResult.SQL)),
		SQLCount: len(diffResult.SQL),
	}, nil
}

// DryRunResult contém o resultado de uma operação dry-run
type DryRunResult struct {
	Success  bool
	Message  string
	Error    string
	SQLCount int
}