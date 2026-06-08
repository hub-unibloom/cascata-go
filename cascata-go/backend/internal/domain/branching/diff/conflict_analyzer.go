package diff

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ConflictAnalysis representa a análise completa de conflitos entre branches
type ConflictAnalysis struct {
	// SourceBranch é a branch de origem (ex: "feat/novo-checkout")
	SourceBranch string `json:"source_branch"`
	
	// TargetBranch é a branch de destino (ex: "main")
	TargetBranch string `json:"target_branch"`
	
	// ProjectSlug identifica o projeto
	ProjectSlug string `json:"project_slug"`
	
	// HasConflicts indica se existem conflitos bloqueantes
	HasConflicts bool `json:"has_conflicts"`
	
	// ConflictCount é o número total de conflitos detectados
	ConflictCount int `json:"conflict_count"`
	
	// SchemaChanges contém todas as mudanças de schema detectadas
	SchemaChanges *SchemaChangeSummary `json:"schema_changes"`
	
	// TenancyImpacts lista impactos no sistema de tenancy
	TenancyImpacts []TenancyImpact `json:"tenancy_impacts"`
	
	// AuthChanges lista mudanças em autenticação/autorização
	AuthChanges []AuthChange `json:"auth_changes"`
	
	// DataLossRisk indica se há risco de perda de dados
	DataLossRisk DataLossRisk `json:"data_loss_risk"`
	
	// BreakingChanges lista mudanças breaking para o frontend/API
	BreakingChanges []BreakingChange `json:"breaking_changes"`
	
	// Recommendations sugere ações para resolver conflitos
	Recommendations []string `json:"recommendations"`
	
	// GeneratedAt é o timestamp da análise
	GeneratedAt time.Time `json:"generated_at"`
	
	// AnalysisDurationMs é o tempo total da análise em milissegundos
	AnalysisDurationMs int64 `json:"analysis_duration_ms"`
}

// SchemaChangeSummary resume todas as mudanças de schema
type SchemaChangeSummary struct {
	// TablesChanged é o número de tabelas afetadas
	TablesChanged int `json:"tables_changed"`
	
	// ColumnsChanged é o número de colunas afetadas
	ColumnsChanged int `json:"columns_changed"`
	
	// IndexesChanged é o número de índices afetados
	IndexesChanged int `json:"indexes_changed"`
	
	// RLSChanged é o número de políticas RLS afetadas
	RLSChanged int `json:"rls_changed"`
	
	// FunctionsChanged é o número de funções/RPCs afetados
	FunctionsChanged int `json:"functions_changed"`
	
	// TriggersChanged é o número de triggers afetados
	TriggersChanged int `json:"triggers_changed"`
	
	// PermissionsChanged é o número de permissões afetadas
	PermissionsChanged int `json:"permissions_changed"`
	
	// NewTables lista tabelas novas
	NewTables []TableChange `json:"new_tables"`
	
	// ModifiedTables lista tabelas modificadas
	ModifiedTables []TableChange `json:"modified_tables"`
	
	// DeletedTables lista tabelas removidas
	DeletedTables []TableChange `json:"deleted_tables"`
}

// TableChange descreve uma mudança em uma tabela
type TableChange struct {
	// TableName é o nome da tabela
	TableName string `json:"table_name"`
	
	// ChangeType é o tipo de mudança (created, modified, deleted)
	ChangeType string `json:"change_type"`
	
	// ColumnChanges lista mudanças nas colunas
	ColumnChanges []ColumnChange `json:"column_changes,omitempty"`
	
	// RLSChanges lista mudanças nas políticas RLS
	RLSChanges []RLSChange `json:"rls_changes,omitempty"`
	
	// ImpactLevel é o nível de impacto (low, medium, high, critical)
	ImpactLevel string `json:"impact_level"`
	
	// Description é uma descrição humana da mudança
	Description string `json:"description"`
}

// ColumnChange descreve uma mudança em uma coluna
type ColumnChange struct {
	// ColumnName é o nome da coluna
	ColumnName string `json:"column_name"`
	
	// ChangeType é o tipo de mudança (added, removed, type_changed, nullability_changed, default_changed)
	ChangeType string `json:"change_type"`
	
	// OldValue é o valor anterior (se aplicável)
	OldValue string `json:"old_value,omitempty"`
	
	// NewValue é o novo valor (se aplicável)
	NewValue string `json:"new_value,omitempty"`
	
	// IsBreaking indica se é uma mudança breaking
	IsBreaking bool `json:"is_breaking"`
	
	// MigrationRequired indica se requer migração de dados
	MigrationRequired bool `json:"migration_required"`
	
	// Description é uma descrição da mudança
	Description string `json:"description"`
}

// RLSChange descreve uma mudança em política RLS
type RLSChange struct {
	// PolicyName é o nome da política
	PolicyName string `json:"policy_name"`
	
	// TableName é a tabela afetada
	TableName string `json:"table_name"`
	
	// ChangeType é o tipo de mudança (created, dropped, modified)
	ChangeType string `json:"change_type"`
	
	// OldPolicy é a definição antiga (se aplicável)
	OldPolicy string `json:"old_policy,omitempty"`
	
	// NewPolicy é a nova definição (se aplicável)
	NewPolicy string `json:"new_policy,omitempty"`
	
	// SecurityImpact descreve o impacto na segurança
	SecurityImpact string `json:"security_impact"`
}

// TenancyImpact descreve um impacto no sistema de tenancy
type TenancyImpact struct {
	// ImpactType é o tipo de impacto (isolation_violation, cross_tenant_access, tenant_schema_change)
	ImpactType string `json:"impact_type"`
	
	// Severity é a severidade (low, medium, high, critical)
	Severity string `json:"severity"`
	
	// Description é uma descrição do impacto
	Description string `json:"description"`
	
	// AffectedTables lista tabelas afetadas
	AffectedTables []string `json:"affected_tables"`
	
	// Mitigation sugere como mitigar o impacto
	Mitigation string `json:"mitigation"`
}

// AuthChange descreve uma mudança em autenticação/autorização
type AuthChange struct {
	// ChangeType é o tipo de mudança (new_auth_table, auth_column_added, rls_policy_changed, permission_changed)
	ChangeType string `json:"change_type"`
	
	// Description é uma descrição da mudança
	Description string `json:"description"`
	
	// AffectedObjects lista objetos afetados (tabelas, colunas, policies)
	AffectedObjects []string `json:"affected_objects"`
	
	// RiskLevel é o nível de risco (low, medium, high)
	RiskLevel string `json:"risk_level"`
	
	// RequiresReview indica se requer revisão manual
	RequiresReview bool `json:"requires_review"`
}

// DataLossRisk avalia o risco de perda de dados
type DataLossRisk struct {
	// HasRisk indica se há risco de perda de dados
	HasRisk bool `json:"has_risk"`
	
	// RiskLevel é o nível de risco (none, low, medium, high, critical)
	RiskLevel string `json:"risk_level"`
	
	// AtRiskTables lista tabelas com dados em risco
	AtRiskTables []string `json:"at_risk_tables,omitempty"`
	
	// AtRiskColumns lista colunas com dados em risco
	AtRiskColumns []string `json:"at_risk_columns,omitempty"`
	
	// EstimatedRowsAffected estima o número de linhas afetadas
	EstimatedRowsAffected int64 `json:"estimated_rows_affected,omitempty"`
	
	// Description descreve o risco
	Description string `json:"description"`
	
	// BackupRecommended indica se backup é recomendado antes do merge
	BackupRecommended bool `json:"backup_recommended"`
}

// BreakingChange lista uma mudança breaking para frontend/API
type BreakingChange struct {
	// ChangeType é o tipo de mudança (column_removed, type_changed, table_removed, api_contract_changed)
	ChangeType string `json:"change_type"`
	
	// Description é uma descrição da mudança
	Description string `json:"description"`
	
	// AffectedObjects lista objetos afetados
	AffectedObjects []string `json:"affected_objects"`
	
	// FrontendImpact descreve o impacto no frontend
	FrontendImpact string `json:"frontend_impact"`
	
	// APIImpact descreve o impacto na API
	APIImpact string `json:"api_impact"`
	
	// MigrationPath sugere como migrar
	MigrationPath string `json:"migration_path"`
}

// ConflictAnalyzer é o analisador principal de conflitos
type ConflictAnalyzer struct {
	ctx DiffContext
}

// NewConflictAnalyzer cria um novo analisador de conflitos
func NewConflictAnalyzer(ctx DiffContext) *ConflictAnalyzer {
	return &ConflictAnalyzer{
		ctx: ctx,
	}
}

// Analyze executa a análise completa de conflitos
func (a *ConflictAnalyzer) Analyze(ctx context.Context) (*ConflictAnalysis, error) {
	startTime := time.Now()
	
	analysis := &ConflictAnalysis{
		SourceBranch: a.ctx.SourceBranch,
		TargetBranch: a.ctx.TargetBranch,
		ProjectSlug:  a.ctx.ProjectSlug,
		GeneratedAt:  startTime,
		SchemaChanges: &SchemaChangeSummary{
			NewTables:       make([]TableChange, 0),
			ModifiedTables:  make([]TableChange, 0),
			DeletedTables:   make([]TableChange, 0),
		},
		TenancyImpacts:    make([]TenancyImpact, 0),
		AuthChanges:       make([]AuthChange, 0),
		BreakingChanges:   make([]BreakingChange, 0),
		Recommendations:   make([]string, 0),
	}
	
	// Executa o diff engine para obter todas as mudanças
	diffEngine := NewDiffEngine(a.ctx)
	diffResult, err := diffEngine.Run(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to run diff: %w", err)
	}
	
	// Processa cada fase do diff para extrair informações detalhadas
	if err := a.processTablesPhase(analysis, diffResult); err != nil {
		return nil, fmt.Errorf("failed to process tables phase: %w", err)
	}
	
	if err := a.processColumnsPhase(analysis, diffResult); err != nil {
		return nil, fmt.Errorf("failed to process columns phase: %w", err)
	}
	
	if err := a.processRLSPhase(analysis, diffResult); err != nil {
		return nil, fmt.Errorf("failed to process RLS phase: %w", err)
	}
	
	if err := a.processFunctionsPhase(analysis, diffResult); err != nil {
		return nil, fmt.Errorf("failed to process functions phase: %w", err)
	}
	
	if err := a.processTriggersPhase(analysis, diffResult); err != nil {
		return nil, fmt.Errorf("failed to process triggers phase: %w", err)
	}
	
	if err := a.processPermissionsPhase(analysis, diffResult); err != nil {
		return nil, fmt.Errorf("failed to process permissions phase: %w", err)
	}
	
	// Analisa impactos de tenancy
	a.analyzeTenancyImpacts(analysis)
	
	// Analisa mudanças de autenticação
	a.analyzeAuthChanges(analysis)
	
	// Avalia risco de perda de dados
	a.assessDataLossRisk(analysis)
	
	// Identifica breaking changes
	a.identifyBreakingChanges(analysis)
	
	// Gera recomendações
	a.generateRecommendations(analysis)
	
	// Calcula totais
	analysis.ConflictCount = analysis.SchemaChanges.TablesChanged +
		analysis.SchemaChanges.ColumnsChanged +
		analysis.SchemaChanges.RLSChanged +
		analysis.SchemaChanges.FunctionsChanged +
		analysis.SchemaChanges.TriggersChanged +
		analysis.SchemaChanges.PermissionsChanged
	
	analysis.HasConflicts = analysis.ConflictCount > 0 || len(analysis.TenancyImpacts) > 0 || len(analysis.AuthChanges) > 0
	analysis.AnalysisDurationMs = time.Since(startTime).Milliseconds()
	
	return analysis, nil
}

// processTablesPhase processa mudanças de tabelas
func (a *ConflictAnalyzer) processTablesPhase(analysis *ConflictAnalysis, diffResult *DiffResult) error {
	for _, summary := range diffResult.Summaries {
		if summary.PhaseName != "tables" {
			continue
		}
		
		for _, detail := range summary.Details {
			if strings.HasPrefix(detail, "Create tables:") {
				tableNames := strings.Split(strings.TrimPrefix(detail, "Create tables: "), ", ")
				for _, tableName := range tableNames {
					if tableName == "" {
						continue
					}
					analysis.SchemaChanges.NewTables = append(analysis.SchemaChanges.NewTables, TableChange{
						TableName:   tableName,
						ChangeType:  "created",
						ImpactLevel: "medium",
						Description: fmt.Sprintf("Nova tabela '%s' será criada", tableName),
					})
					analysis.SchemaChanges.TablesChanged++
				}
			} else if strings.HasPrefix(detail, "Drop tables:") {
				tableNames := strings.Split(strings.TrimPrefix(detail, "Drop tables: "), ", ")
				for _, tableName := range tableNames {
					if tableName == "" {
						continue
					}
					analysis.SchemaChanges.DeletedTables = append(analysis.SchemaChanges.DeletedTables, TableChange{
						TableName:   tableName,
						ChangeType:  "deleted",
						ImpactLevel: "high",
						Description: fmt.Sprintf("Tabela '%s' será removida (CUIDADO: perda de dados!)", tableName),
					})
					analysis.SchemaChanges.TablesChanged++
				}
			}
		}
	}
	return nil
}

// processColumnsPhase processa mudanças de colunas
func (a *ConflictAnalyzer) processColumnsPhase(analysis *ConflictAnalysis, diffResult *DiffResult) error {
	for _, summary := range diffResult.Summaries {
		if summary.PhaseName != "columns" {
			continue
		}
		
		for _, detail := range summary.Details {
			if strings.HasPrefix(detail, "Add columns to") {
				parts := strings.Split(detail, ": ")
				if len(parts) == 2 {
					tableName := strings.TrimPrefix(parts[0], "Add columns to ")
					colCount := 0
					fmt.Sscanf(parts[1], "%d", &colCount)
					
					analysis.SchemaChanges.ModifiedTables = appendUniqueTable(
						analysis.SchemaChanges.ModifiedTables,
						tableName,
						"modified",
					)
					analysis.SchemaChanges.ColumnsChanged += colCount
				}
			} else if strings.HasPrefix(detail, "Alter columns in") {
				parts := strings.Split(detail, ": ")
				if len(parts) == 2 {
					tableName := strings.TrimPrefix(parts[0], "Alter columns in ")
					colCount := 0
					fmt.Sscanf(parts[1], "%d", &colCount)
					
					analysis.SchemaChanges.ModifiedTables = appendUniqueTable(
						analysis.SchemaChanges.ModifiedTables,
						tableName,
						"modified",
					)
					analysis.SchemaChanges.ColumnsChanged += colCount
				}
			} else if strings.HasPrefix(detail, "Drop columns from") {
				parts := strings.Split(detail, ": ")
				if len(parts) == 2 {
					tableName := strings.TrimPrefix(parts[0], "Drop columns from ")
					colCount := 0
					fmt.Sscanf(parts[1], "%d", &colCount)
					
					analysis.SchemaChanges.ModifiedTables = appendUniqueTable(
						analysis.SchemaChanges.ModifiedTables,
						tableName,
						"modified",
					)
					analysis.SchemaChanges.ColumnsChanged += colCount
				}
			}
		}
	}
	return nil
}

// processRLSPhase processa mudanças de RLS
func (a *ConflictAnalyzer) processRLSPhase(analysis *ConflictAnalysis, diffResult *DiffResult) error {
	for _, summary := range diffResult.Summaries {
		if summary.PhaseName == "rls" {
			analysis.SchemaChanges.RLSChanged = summary.Changes
		}
	}
	return nil
}

// processFunctionsPhase processa mudanças de funções
func (a *ConflictAnalyzer) processFunctionsPhase(analysis *ConflictAnalysis, diffResult *DiffResult) error {
	for _, summary := range diffResult.Summaries {
		if summary.PhaseName == "functions" {
			analysis.SchemaChanges.FunctionsChanged = summary.Changes
		}
	}
	return nil
}

// processTriggersPhase processa mudanças de triggers
func (a *ConflictAnalyzer) processTriggersPhase(analysis *ConflictAnalysis, diffResult *DiffResult) error {
	for _, summary := range diffResult.Summaries {
		if summary.PhaseName == "triggers" {
			analysis.SchemaChanges.TriggersChanged = summary.Changes
		}
	}
	return nil
}

// processPermissionsPhase processa mudanças de permissões
func (a *ConflictAnalyzer) processPermissionsPhase(analysis *ConflictAnalysis, diffResult *DiffResult) error {
	for _, summary := range diffResult.Summaries {
		if summary.PhaseName == "permissions" {
			analysis.SchemaChanges.PermissionsChanged = summary.Changes
		}
	}
	return nil
}

// analyzeTenancyImpacts analisa impactos no sistema de tenancy
func (a *ConflictAnalyzer) analyzeTenancyImpacts(analysis *ConflictAnalysis) {
	// Verifica se há tabelas com sufixo de tenant sendo criadas/modificadas
	for _, table := range analysis.SchemaChanges.NewTables {
		if strings.Contains(table.TableName, "_tenant_") || strings.HasSuffix(table.TableName, "_tenant") {
			analysis.TenancyImpacts = append(analysis.TenancyImpacts, TenancyImpact{
				ImpactType:     "tenant_schema_change",
				Severity:       "medium",
				Description:    fmt.Sprintf("Nova tabela de tenant '%s' detectada", table.TableName),
				AffectedTables: []string{table.TableName},
				Mitigation:     "Verificar se a tabela está corretamente isolada por tenant",
			})
		}
	}
	
	// Verifica se há colunas de tenant_id sendo adicionadas/removidas
	for _, table := range analysis.SchemaChanges.ModifiedTables {
		for _, colChange := range table.ColumnChanges {
			if colChange.ColumnName == "tenant_id" || strings.Contains(colChange.ColumnName, "tenant") {
				impactType := "cross_tenant_access"
				if colChange.ChangeType == "added" {
					impactType = "isolation_violation"
				}
				
				analysis.TenancyImpacts = append(analysis.TenancyImpacts, TenancyImpact{
					ImpactType:     impactType,
					Severity:       "high",
					Description:    fmt.Sprintf("Coluna de tenant '%s' %s na tabela '%s'", colChange.ColumnName, colChange.ChangeType, table.TableName),
					AffectedTables: []string{table.TableName},
					Mitigation:     "Revisar políticas RLS para garantir isolamento correto",
				})
			}
		}
	}
}

// analyzeAuthChanges analisa mudanças em autenticação/autorização
func (a *ConflictAnalyzer) analyzeAuthChanges(analysis *ConflictAnalysis) {
	authTables := []string{"users", "roles", "permissions", "user_roles", "role_permissions", "auth", "sessions"}
	
	for _, table := range analysis.SchemaChanges.NewTables {
		for _, authTable := range authTables {
			if strings.Contains(strings.ToLower(table.TableName), authTable) {
				analysis.AuthChanges = append(analysis.AuthChanges, AuthChange{
					ChangeType:      "new_auth_table",
					Description:     fmt.Sprintf("Nova tabela de autenticação '%s' detectada", table.TableName),
					AffectedObjects: []string{table.TableName},
					RiskLevel:       "high",
					RequiresReview:  true,
				})
			}
		}
	}
	
	// Verifica mudanças em RLS que podem afetar autenticação
	if analysis.SchemaChanges.RLSChanged > 0 {
		analysis.AuthChanges = append(analysis.AuthChanges, AuthChange{
			ChangeType:      "rls_policy_changed",
			Description:     fmt.Sprintf("%d políticas RLS foram modificadas - pode afetar controle de acesso", analysis.SchemaChanges.RLSChanged),
			AffectedObjects: []string{"rls_policies"},
			RiskLevel:       "medium",
			RequiresReview:  true,
		})
	}
}

// assessDataLossRisk avalia risco de perda de dados
func (a *ConflictAnalyzer) assessDataLossRisk(analysis *ConflictAnalysis) {
	risk := DataLossRisk{
		HasRisk:             false,
		RiskLevel:           "none",
		AtRiskTables:        make([]string, 0),
		AtRiskColumns:       make([]string, 0),
		BackupRecommended:   false,
	}
	
	// Tabelas deletadas são alto risco
	if len(analysis.SchemaChanges.DeletedTables) > 0 {
		risk.HasRisk = true
		risk.RiskLevel = "critical"
		risk.BackupRecommended = true
		
		for _, table := range analysis.SchemaChanges.DeletedTables {
			risk.AtRiskTables = append(risk.AtRiskTables, table.TableName)
		}
		
		risk.Description = fmt.Sprintf("%d tabela(s) serão removidas - dados serão permanentemente perdidos", len(analysis.SchemaChanges.DeletedTables))
	}
	
	// Colunas removidas são médio/alto risco
	columnDrops := 0
	for _, table := range analysis.SchemaChanges.ModifiedTables {
		for _, colChange := range table.ColumnChanges {
			if colChange.ChangeType == "removed" {
				columnDrops++
				risk.AtRiskColumns = append(risk.AtRiskColumns, fmt.Sprintf("%s.%s", table.TableName, colChange.ColumnName))
				
				if risk.RiskLevel == "none" {
					risk.HasRisk = true
					risk.RiskLevel = "medium"
					risk.BackupRecommended = true
				} else if risk.RiskLevel == "medium" {
					risk.RiskLevel = "high"
				}
			}
		}
	}
	
	if columnDrops > 0 && risk.Description == "" {
		risk.Description = fmt.Sprintf("%d coluna(s) serão removidas - dados nessas colunas serão perdidos", columnDrops)
	}
	
	analysis.DataLossRisk = risk
}

// identifyBreakingChanges identifica breaking changes para frontend/API
func (a *ConflictAnalyzer) identifyBreakingChanges(analysis *ConflictAnalysis) {
	// Tabelas removidas são breaking changes críticas
	for _, table := range analysis.SchemaChanges.DeletedTables {
		analysis.BreakingChanges = append(analysis.BreakingChanges, BreakingChange{
			ChangeType:      "table_removed",
			Description:     fmt.Sprintf("Tabela '%s' foi removida", table.TableName),
			AffectedObjects: []string{table.TableName},
			FrontendImpact:  "Qualquer query ou componente usando esta tabela irá falhar",
			APIImpact:       "Endpoints que retornam dados desta tabela retornarão erro",
			MigrationPath:   "Remover referências no frontend e atualizar endpoints da API",
		})
	}
	
	// Colunas removidas são breaking changes
	for _, table := range analysis.SchemaChanges.ModifiedTables {
		for _, colChange := range table.ColumnChanges {
			if colChange.ChangeType == "removed" {
				analysis.BreakingChanges = append(analysis.BreakingChanges, BreakingChange{
					ChangeType:      "column_removed",
					Description:     fmt.Sprintf("Coluna '%s' foi removida da tabela '%s'", colChange.ColumnName, table.TableName),
					AffectedObjects: []string{fmt.Sprintf("%s.%s", table.TableName, colChange.ColumnName)},
					FrontendImpact:  "Campos/formulários usando esta coluna irão falhar",
					APIImpact:       "Respostas da API não incluirão mais este campo",
					MigrationPath:   "Atualizar frontend para não usar esta coluna e versionar API",
				})
			} else if colChange.ChangeType == "type_changed" && colChange.IsBreaking {
				analysis.BreakingChanges = append(analysis.BreakingChanges, BreakingChange{
					ChangeType:      "type_changed",
					Description:     fmt.Sprintf("Tipo da coluna '%s' mudou de '%s' para '%s'", colChange.ColumnName, colChange.OldValue, colChange.NewValue),
					AffectedObjects: []string{fmt.Sprintf("%s.%s", table.TableName, colChange.ColumnName)},
					FrontendImpact:  "Validações e parsing no frontend podem falhar",
					APIImpact:       "Formato dos dados na API mudou - clientes precisam ser atualizados",
					MigrationPath:   "Implementar transformação de dados no backend ou versionar API",
				})
			}
		}
	}
}

// generateRecommendations gera recomendações baseadas na análise
func (a *ConflictAnalyzer) generateRecommendations(analysis *ConflictAnalysis) {
	recommendations := make([]string, 0)
	
	// Recomendações baseadas em riscos de dados
	if analysis.DataLossRisk.HasRisk {
		if analysis.DataLossRisk.BackupRecommended {
			recommendations = append(recommendations, "⚠️ CRÍTICO: Faça backup completo do banco antes deste merge")
		}
		if analysis.DataLossRisk.RiskLevel == "critical" {
			recommendations = append(recommendations, "🛑 ALTO RISCO: Este merge causará perda permanente de dados - revise cuidadosamente")
		}
	}
	
	// Recomendações baseadas em breaking changes
	if len(analysis.BreakingChanges) > 0 {
		recommendations = append(recommendations, fmt.Sprintf("📢 BREAKING CHANGES: %d mudança(s) quebrarão compatibilidade - comunique aos consumidores da API", len(analysis.BreakingChanges)))
		recommendations = append(recommendations, "Considere versionar a API ou implementar migração gradual")
	}
	
	// Recomendações baseadas em auth changes
	for _, authChange := range analysis.AuthChanges {
		if authChange.RequiresReview {
			recommendations = append(recommendations, fmt.Sprintf("🔐 SEGURANÇA: %s - Requer revisão manual de segurança", authChange.Description))
		}
	}
	
	// Recomendações baseadas em tenancy impacts
	for _, impact := range analysis.TenancyImpacts {
		if impact.Severity == "high" || impact.Severity == "critical" {
			recommendations = append(recommendations, fmt.Sprintf("🏢 TENANCY: %s - %s", impact.Description, impact.Mitigation))
		}
	}
	
	// Recomendações gerais baseadas no escopo
	totalChanges := analysis.SchemaChanges.TablesChanged + analysis.SchemaChanges.ColumnsChanged
	if totalChanges > 10 {
		recommendations = append(recommendations, fmt.Sprintf("📊 ESCOPO GRANDE: %d mudanças no total - considere dividir em merges menores", totalChanges))
	}
	
	if analysis.SchemaChanges.RLSChanged > 0 {
		recommendations = append(recommendations, "🔒 RLS: Políticas de segurança foram alteradas - teste exaustivamente o controle de acesso")
	}
	
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "✅ Nenhuma recomendação especial - merge parece seguro")
	}
	
	analysis.Recommendations = recommendations
}

// appendUniqueTable adiciona uma tabela única à lista
func appendUniqueTable(tables []TableChange, tableName, changeType string) []TableChange {
	// Verifica se já existe
	for _, t := range tables {
		if t.TableName == tableName {
			// Já existe, retorna como está
			return tables
		}
	}
	
	// Não existe, adiciona
	return append(tables, TableChange{
		TableName:   tableName,
		ChangeType:  changeType,
		ImpactLevel: "medium",
		Description: fmt.Sprintf("Tabela '%s' foi modificada", tableName),
	})
}
