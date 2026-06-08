/**
 * ColumnImpactScanner — Enterprise Dependency Discovery Engine
 *
 * Scans 9 PostgreSQL catalog sources to find every object
 * referencing a given column. Used by the Protocolo Cascata
 * before rename/delete operations.
 */

export interface DependencyItem {
    type: 'fk' | 'index' | 'view' | 'trigger' | 'function' | 'policy' | 'cronjob' | 'check' | 'sequence';
    name: string;
    detail: string;
    cascadeSQL?: string;
    severity: 'info' | 'warning' | 'danger';
}

type FetchFn = (url: string, options?: any) => Promise<any>;

/**
 * Smart Name Generator: Sugere novos nomes para objetos dependentes (FKs, Indexes, Policies)
 * baseados no novo nome da coluna para manter a consistência do esquema.
 */
function suggestNewName(currentName: string, oldCol: string, newCol: string): string {
    const regex = new RegExp(`(^|_)${oldCol}(_|$)`, 'g');
    if (regex.test(currentName)) {
        return currentName.replace(regex, `$1${newCol}$2`);
    }
    return currentName;
}

// Helper: run a catalog query and return rows (empty array on failure)
async function catalogQuery(
    fetchWithAuth: FetchFn,
    projectId: string,
    schema: string,
    sql: string
): Promise<any[]> {
    try {
        const res = await fetchWithAuth(`/api/data/${projectId}/query?schema=${schema}`, {
            method: 'POST',
            body: JSON.stringify({ sql }),
        });
        return res.rows || [];
    } catch {
        return [];
    }
}

/**
 * Scan ALL dependencies of a column across the entire database.
 *
 * EXECUÇÃO SEQUENCIAL: As queries rodam uma por vez para evitar
 * race conditions com prepared statements no PgBouncer.
 */
export async function scanColumnDependencies(
    fetchWithAuth: FetchFn,
    projectId: string,
    schema: string,
    table: string,
    column: string,
    action: 'rename' | 'delete',
    newName?: string
): Promise<DependencyItem[]> {
    const deps: DependencyItem[] = [];

    // SECURITY: Escapar variáveis no topo para uso em todas as queries
    const escapedColumn = column.replace(/'/g, "''");
    const escapedTable = table.replace(/'/g, "''");
    const escapedSchema = schema.replace(/'/g, "''");

    // ── 1. Foreign Keys ─────────────────────────────────────────
    const fkRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT
      con.conname AS constraint_name,
      src_rel.relname AS source_table,
      src_att.attname AS source_column,
      tgt_rel.relname AS target_table,
      tgt_att.attname AS target_column
    FROM pg_constraint con
    JOIN pg_class src_rel ON con.conrelid = src_rel.oid
    JOIN pg_namespace src_ns ON src_rel.relnamespace = src_ns.oid
    JOIN pg_class tgt_rel ON con.confrelid = tgt_rel.oid
    JOIN pg_attribute src_att ON src_att.attrelid = con.conrelid
      AND src_att.attnum = ANY(con.conkey)
    JOIN pg_attribute tgt_att ON tgt_att.attrelid = con.confrelid
      AND tgt_att.attnum = ANY(con.confkey)
    WHERE con.contype = 'f'
      AND src_ns.nspname = '${escapedSchema}'
      AND (
        (tgt_rel.relname = '${escapedTable}' AND tgt_att.attname = '${escapedColumn}')
        OR (src_rel.relname = '${escapedTable}' AND src_att.attname = '${escapedColumn}')
      )
  `);

    for (const fk of fkRows) {
        const isTarget = fk.target_table === table && fk.target_column === column;
        const detail = isTarget
            ? `${fk.source_table}.${fk.source_column} → ${table}.${column}`
            : `${table}.${column} → ${fk.target_table}.${fk.target_column}`;

        let cascadeSQL: string | undefined;
        if (action === 'delete') {
            cascadeSQL = `ALTER TABLE ${schema}."${fk.source_table}" DROP CONSTRAINT "${fk.constraint_name}";`;
        } else if (action === 'rename' && newName) {
            // PostgreSQL auto-atualiza a referência interna, mas NÃO o nome da constraint.
            // Sugerimos renomear a constraint para evitar nomes mentirosos (ex: id_fkey apontando para user_id).
            const suggestedName = suggestNewName(fk.constraint_name, column, newName);
            if (suggestedName !== fk.constraint_name) {
                cascadeSQL = `ALTER TABLE ${schema}."${fk.source_table}" RENAME CONSTRAINT "${fk.constraint_name}" TO "${suggestedName}";`;
            }
        }

        deps.push({
            type: 'fk',
            name: fk.constraint_name,
            detail: action === 'rename' && cascadeSQL
                ? `${detail} (Suggesting Rename to "${suggestNewName(fk.constraint_name, column, newName!)}")`
                : detail,
            cascadeSQL,
            severity: 'danger',
        });
    }

    // ── 2. Indexes ──────────────────────────────────────────────
    const indexRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT
      i.relname AS index_name,
      a.attname AS column_name,
      ix.indisunique AS is_unique,
      pg_get_indexdef(ix.indexrelid) AS index_def
    FROM pg_index ix
    JOIN pg_class t ON t.oid = ix.indrelid
    JOIN pg_class i ON i.oid = ix.indexrelid
    JOIN pg_namespace n ON t.relnamespace = n.oid
    JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
    WHERE n.nspname = '${escapedSchema}'
      AND t.relname = '${escapedTable}'
      AND a.attname = '${escapedColumn}'
  `);

    for (const idx of indexRows) {
        let cascadeSQL: string | undefined;
        const suggestedName = newName ? suggestNewName(idx.index_name, column, newName) : idx.index_name;

        if (action === 'delete') {
            cascadeSQL = `DROP INDEX IF EXISTS ${schema}."${idx.index_name}";`;
        } else if (action === 'rename' && newName) {
            // SENIOR MOVE: Não use DROP/CREATE se apenas o nome mudar.
            // PostgreSQL já atualiza a definição do índice internamente.
            if (suggestedName !== idx.index_name) {
                cascadeSQL = `ALTER INDEX ${schema}."${idx.index_name}" RENAME TO "${suggestedName}";`;
            }
        }

        deps.push({
            type: 'index',
            name: idx.index_name,
            detail: `${idx.is_unique ? 'UNIQUE ' : ''}INDEX on ${table}(${column})`,
            cascadeSQL,
            severity: 'warning',
        });
    }

    // ── 3. Views ────────────────────────────────────────────────
    // Usa pg_depend com refobjsubid para filtrar pela coluna exata no nível de atributo,
    // evitando falsos positivos por ILIKE em nomes similares.
    const viewRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT DISTINCT
      v.relname AS view_name,
      pg_get_viewdef(v.oid, true) AS view_def
    FROM pg_depend d
    JOIN pg_rewrite r ON r.oid = d.objid
    JOIN pg_class v ON v.oid = r.ev_class
    JOIN pg_class t ON t.oid = d.refobjid
    JOIN pg_namespace n ON v.relnamespace = n.oid
    JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
    WHERE d.deptype = 'n'
      AND v.relkind = 'v'
      AND t.relname = '${escapedTable}'
      AND n.nspname = '${escapedSchema}'
      AND a.attname = '${escapedColumn}'
  `);

    for (const v of viewRows) {
        let cascadeSQL: string | undefined;
        if (action === 'delete') {
            cascadeSQL = `DROP VIEW IF EXISTS ${schema}."${v.view_name}" CASCADE;`;
        } else if (action === 'rename' && newName) {
            const newDef = (v.view_def || '').replace(
                new RegExp(`\\b${column}\\b`, 'g'),
                newName
            );
            cascadeSQL = `CREATE OR REPLACE VIEW ${schema}."${v.view_name}" AS ${newDef}`;
        }

        deps.push({
            type: 'view',
            name: v.view_name,
            detail: `View references "${column}" in definition`,
            cascadeSQL,
            severity: 'warning',
        });
    }

    // ── 4. Triggers ─────────────────────────────────────────────
    // Filtra no SQL apenas triggers que referenciam a coluna explicitamente na definição.
    const triggerRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT
      t.tgname AS trigger_name,
      pg_get_triggerdef(t.oid, true) AS trigger_def
    FROM pg_trigger t
    JOIN pg_class c ON c.oid = t.tgrelid
    JOIN pg_namespace n ON c.relnamespace = n.oid
    WHERE n.nspname = '${escapedSchema}'
      AND c.relname = '${escapedTable}'
      AND NOT t.tgisinternal
      AND pg_get_triggerdef(t.oid, true) ILIKE '%${escapedColumn}%'
  `);

    for (const tr of triggerRows) {
        deps.push({
            type: 'trigger',
            name: tr.trigger_name,
            detail: `Trigger references "${column}" in definition`,
            cascadeSQL: action === 'delete'
                ? `DROP TRIGGER IF EXISTS "${tr.trigger_name}" ON ${schema}."${table}";`
                : undefined,
            severity: 'info',
        });
    }

    // ── 5. Functions / RPCs ─────────────────────────────────────
    // Filtra apenas funções SQL normais, excluindo agregadas/internas/window
    // para evitar erro "commit unexpectedly resulted in rollback" no PgBouncer.
    const funcRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT
      p.proname AS func_name,
      n.nspname AS func_schema,
      COALESCE(pg_get_functiondef(p.oid), p.prosrc) AS func_def
    FROM pg_proc p
    JOIN pg_namespace n ON p.pronamespace = n.oid
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND p.prokind = 'f'        -- Apenas funções normais (não agregadas 'a', procedures 'p', window 'w')
      AND p.prolang != 12        -- Exclui funções internas (lang=12 é internal)
      AND p.prosrc IS NOT NULL   -- Garante que há código fonte
      AND (
        COALESCE(pg_get_functiondef(p.oid), p.prosrc) ILIKE '%${escapedColumn}%'
        OR p.prosrc ILIKE '%${escapedColumn}%'
      )
      AND (
        COALESCE(pg_get_functiondef(p.oid), p.prosrc) ILIKE '%${escapedTable}%'
        OR p.prosrc ILIKE '%${escapedTable}%'
      )
  `);

    for (const fn of funcRows) {
        deps.push({
            type: 'function',
            name: `${fn.func_schema}.${fn.func_name}`,
            detail: `Function body references "${table}.${column}" — ⚠ Manual review required`,
            cascadeSQL: undefined, // Cannot auto-fix function bodies safely
            severity: 'warning',
        });
    }

    // ── 6. RLS Policies ─────────────────────────────────────────
    const policyRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT
      pol.polname AS policy_name,
      pg_get_expr(pol.polqual, pol.polrelid, true) AS using_expr,
      pg_get_expr(pol.polwithcheck, pol.polrelid, true) AS check_expr,
      CASE pol.polcmd
        WHEN 'r' THEN 'SELECT'
        WHEN 'a' THEN 'INSERT'
        WHEN 'w' THEN 'UPDATE'
        WHEN 'd' THEN 'DELETE'
        WHEN '*' THEN 'ALL'
      END AS command,
      ARRAY(SELECT rolname FROM pg_roles WHERE oid = ANY(pol.polroles)) AS roles
    FROM pg_policy pol
    JOIN pg_class c ON c.oid = pol.polrelid
    JOIN pg_namespace n ON c.relnamespace = n.oid
    WHERE n.nspname = '${escapedSchema}'
      AND c.relname = '${escapedTable}'
      AND (
        pg_get_expr(pol.polqual, pol.polrelid, true) ILIKE '%${escapedColumn}%'
        OR pg_get_expr(pol.polwithcheck, pol.polrelid, true) ILIKE '%${escapedColumn}%'
      )
  `);

    for (const pol of policyRows) {
        let cascadeSQL: string | undefined;
        const suggestedName = newName ? suggestNewName(pol.policy_name, column, newName) : pol.policy_name;

        if (action === 'delete') {
            cascadeSQL = `DROP POLICY IF EXISTS "${pol.policy_name}" ON ${schema}."${table}";`;
        } else if (action === 'rename' && newName) {
            // Recriamos para garantir que a expressão visual e o nome fiquem atualizados
            const newUsing = (pol.using_expr || '').replace(
                new RegExp(`\\b${column}\\b`, 'g'),
                newName
            );
            const newCheck = (pol.check_expr || '').replace(
                new RegExp(`\\b${column}\\b`, 'g'),
                newName
            );
            const roles = (pol.roles || []).join(', ') || 'PUBLIC';

            cascadeSQL = `-- Update RLS Policy: ${pol.policy_name} -> ${suggestedName}\n`;
            cascadeSQL += `DROP POLICY IF EXISTS "${pol.policy_name}" ON ${schema}."${table}";\n`;
            cascadeSQL += `CREATE POLICY "${suggestedName}" ON ${schema}."${table}" FOR ${pol.command} TO ${roles}`;
            if (newUsing && newUsing !== 'NULL') cascadeSQL += ` USING (${newUsing})`;
            if (newCheck && newCheck !== 'NULL') cascadeSQL += ` WITH CHECK (${newCheck})`;
            cascadeSQL += ';';
        }

        deps.push({
            type: 'policy',
            name: pol.policy_name,
            detail: `RLS Policy (${pol.command}) references "${column}"`,
            cascadeSQL,
            severity: 'danger',
        });
    }

    // ── 7. Check Constraints ────────────────────────────────────
    // Usa subquery via pg_class para evitar falha silenciosa do ::regclass
    // quando schema/tabela contém aspas simples escapadas.
    const checkRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT
      conname AS constraint_name,
      pg_get_constraintdef(oid) AS definition
    FROM pg_constraint
    WHERE contype = 'c'
      AND conrelid = (
        SELECT c.oid FROM pg_class c
        JOIN pg_namespace n ON c.relnamespace = n.oid
        WHERE c.relname = '${escapedTable}' AND n.nspname = '${escapedSchema}'
      )
      AND pg_get_constraintdef(oid) ILIKE '%${escapedColumn}%'
  `);

    for (const con of checkRows) {
        let cascadeSQL: string | undefined;
        if (action === 'delete') {
            cascadeSQL = `ALTER TABLE ${schema}."${table}" DROP CONSTRAINT "${con.constraint_name}";`;
        } else if (action === 'rename' && newName) {
            const suggestedName = suggestNewName(con.constraint_name, column, newName);
            const newDef = con.definition.replace(new RegExp(`\\b${column}\\b`, 'g'), newName);

            cascadeSQL = `ALTER TABLE ${schema}."${table}" DROP CONSTRAINT "${con.constraint_name}";\n`;
            cascadeSQL += `ALTER TABLE ${schema}."${table}" ADD CONSTRAINT "${suggestedName}" ${newDef};`;
        }

        deps.push({
            type: 'check',
            name: con.constraint_name,
            detail: `CHECK Constraint references "${column}"`,
            cascadeSQL,
            severity: 'warning',
        });
    }

    // ── 8. Sequences (SERIAL columns) ───────────────────────────
    const seqRows = await catalogQuery(fetchWithAuth, projectId, schema, `
    SELECT
      s.relname AS sequence_name
    FROM pg_depend d
    JOIN pg_class s ON d.objid = s.oid
    JOIN pg_class t ON d.refobjid = t.oid
    JOIN pg_attribute a ON (d.refobjid = a.attrelid AND d.refobjsubid = a.attnum)
    WHERE d.deptype = 'a' -- internal dependency
      AND s.relkind = 'S' -- sequence
      AND t.relname = '${escapedTable}'
      AND a.attname = '${escapedColumn}'
  `);

    for (const seq of seqRows) {
        if (action === 'rename' && newName) {
            const suggestedName = suggestNewName(seq.sequence_name, column, newName);
            if (suggestedName !== seq.sequence_name) {
                deps.push({
                    type: 'sequence',
                    name: seq.sequence_name,
                    detail: `Sequence owned by column "${column}"`,
                    cascadeSQL: `ALTER SEQUENCE ${schema}."${seq.sequence_name}" RENAME TO "${suggestedName}";`,
                    severity: 'info',
                });
            }
        } else if (action === 'delete') {
            // O CASCADE no DROP COLUMN não remove a sequence automaticamente em todas as versões.
            // Reportamos para que o usuário decida se quer limpá-la manualmente.
            deps.push({
                type: 'sequence',
                name: seq.sequence_name,
                detail: `Sequence owned by "${column}" — will become orphaned after column drop`,
                cascadeSQL: `DROP SEQUENCE IF EXISTS ${schema}."${seq.sequence_name}";`,
                severity: 'warning',
            });
        }
    }

    // ── 9. pg_cron Jobs ─────────────────────────────────────────
    // Verifica se a extensão existe antes de consultar (evita erro 42P01)
    const hasCron = await catalogQuery(fetchWithAuth, projectId, schema, `
      SELECT 1 FROM pg_extension WHERE extname = 'pg_cron'
    `);

    let cronRows: any[] = [];
    if (hasCron.length > 0) {
        cronRows = await catalogQuery(fetchWithAuth, projectId, schema, `
        SELECT jobid, jobname, schedule, command
        FROM cron.job
        WHERE command ILIKE '%${escapedTable}%'
          AND command ILIKE '%${escapedColumn}%'
      `);
    }

    for (const cj of cronRows) {
        let cascadeSQL: string | undefined;
        if (action === 'rename' && newName) {
            const newCmd = (cj.command || '').replace(
                new RegExp(`\\b${column}\\b`, 'g'),
                newName
            );
            cascadeSQL = `SELECT cron.alter_job(${cj.jobid}, command := '${newCmd.replace(/'/g, "''")}');`;
        } else if (action === 'delete') {
            cascadeSQL = `SELECT cron.unschedule(${cj.jobid});`;
        }

        deps.push({
            type: 'cronjob',
            name: cj.jobname || `job_${cj.jobid}`,
            detail: `Cron: "${cj.schedule}" — ${(cj.command || '').substring(0, 80)}...`,
            cascadeSQL,
            severity: 'warning',
        });
    }

    return deps;
}

/**
 * Generate the full transactional SQL for a cascade operation.
 *
 * Para DELETE: lista as dependências como comentários informativos — o CASCADE
 * na coluna já resolve a maioria automaticamente, evitando erros de "object does not exist".
 * Para RENAME: emite os SQLs de pre-cascata antes do ALTER COLUMN.
 */
export function buildCascadeSQL(
    schema: string,
    table: string,
    column: string,
    action: 'rename' | 'delete',
    newName: string | undefined,
    dependencies: DependencyItem[]
): string {
    const lines: string[] = [];
    lines.push('BEGIN;');
    lines.push('');

    const isDelete = action === 'delete';
    const preCascade = dependencies.filter(d => d.cascadeSQL);

    if (preCascade.length > 0) {
        if (isDelete) {
            lines.push('-- Note: Dependent objects (FKs, Indexes, etc.) will be automatically removed by CASCADE.');
            lines.push('-- The following list shows what will be affected:');
            for (const dep of preCascade) {
                lines.push(`-- [${dep.type.toUpperCase()}] ${dep.name}`);
            }
            lines.push('');
        } else {
            lines.push('-- Cascade: Update dependent objects');
            for (const dep of preCascade) {
                lines.push(`-- [${dep.type.toUpperCase()}] ${dep.name}`);
                lines.push(dep.cascadeSQL!);
                lines.push('');
            }
        }
    }

    // Main operation
    if (action === 'rename' && newName) {
        lines.push('-- Main: Rename column');
        lines.push(`ALTER TABLE ${schema}."${table}" RENAME COLUMN "${column}" TO "${newName}";`);
    } else if (isDelete) {
        lines.push('-- Main: Drop column');
        lines.push(`ALTER TABLE ${schema}."${table}" DROP COLUMN "${column}" CASCADE;`);
    }

    lines.push('');
    lines.push('COMMIT;');

    return lines.join('\n');
}