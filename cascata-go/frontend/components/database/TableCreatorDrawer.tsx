
import React, { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { X, Plus, Loader2, Link as LinkIcon, Shield, ShieldOff, ShieldCheck, Regex, Cpu, Lock, EyeOff, Calculator, ChevronDown, ChevronUp, Sparkles, User } from 'lucide-react';

// ============================================================
// TableCreatorDrawer — Enterprise Schema Designer
// ============================================================
// Generates idempotent, conflict-free SQL with professional formatting.
// Smart quoting: text defaults auto-wrapped, functions/numbers stay bare.
// ============================================================

interface ColumnDef {
    id: string;
    name: string;
    type: string;
    defaultValue: string;
    isPrimaryKey: boolean;
    isNullable: boolean;
    isUnique: boolean;
    isArray: boolean;
    identityGeneration?: 'always' | 'by_default'; // GENERATED {ALWAYS|BY DEFAULT} AS IDENTITY (PG10+)
    foreignKey?: { schema: string; table: string; column: string };
    sourceHeader?: string;
    description?: string;
    formatPreset?: string;
    formatPattern?: string;
    lockLevel?: 'unlocked' | 'immutable' | 'insert_only' | 'service_role_only' | 'code_protected' | 'otp_protected' | 'auto_clock';
    allowedFactors?: string[]; // Array of factors like ['totp', 'biometria', 'custom_whatsapp']
    maskLevel?: 'unmasked' | 'hide' | 'blur' | 'mask' | 'semi-mask' | 'encrypt';
    formula?: string;
    returnType?: 'text' | 'int' | 'float' | 'numeric' | 'money' | 'boolean' | 'timestamp' | 'date';
    strictMode?: boolean; // If true, formula errors fail the operation; if false, errors = NULL
    autoUser?: boolean; // If true, column defaults to auth.uid() with FK to auth.users(id)
}

interface EnumType {
    name: string;
    schema: string;
    values: string[];
}

interface TableSecurityConfig {
    operations: string[];
    allowed_factors: string[];
}

// Types that support GENERATED AS IDENTITY (only pure integer types — NOT serial/bigserial)
const IDENTITY_COMPATIBLE_TYPES = new Set(['int2', 'int4', 'int8']);

// Types that already have built-in auto-increment (legacy — identity not needed)
const SERIAL_TYPES = new Set(['serial', 'bigserial', 'smallserial']);

// Format presets for column validation (mirrored from backend)
const FORMAT_PRESETS: Record<string, { label: string; regex: string; example: string }> = {
    email: { label: 'Email', regex: '^[a-zA-Z0-9._%+\\-]+@[a-zA-Z0-9.\\-]+\\.[a-zA-Z]{2,}$', example: 'user@example.com' },
    cpf: { label: 'CPF', regex: '^\\d{3}\\.?\\d{3}\\.?\\d{3}-?\\d{2}$', example: '123.456.789-00' },
    cnpj: { label: 'CNPJ', regex: '^\\d{2}\\.?\\d{3}\\.?\\d{3}\\/?\\d{4}-?\\d{2}$', example: '12.345.678/0001-99' },
    phone_br: { label: 'Phone (BR)', regex: '^\\+?55\\s?\\(?\\d{2}\\)?\\s?\\d{4,5}-?\\d{4}$', example: '+55 (11) 99999-1234' },
    cep: { label: 'CEP', regex: '^\\d{5}-?\\d{3}$', example: '01310-100' },
    url: { label: 'URL', regex: '^https?:\\/\\/[a-zA-Z0-9\\-]+(\\.[a-zA-Z0-9\\-]+)+(\\/.*)?$', example: 'https://example.com' },
    uuid_format: { label: 'UUID', regex: '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$', example: 'a1b2c3d4-e5f6-7890-abcd-ef1234567890' },
    date_br: { label: 'Date (BR)', regex: '^\\d{2}\\/\\d{2}\\/\\d{4}$', example: '25/02/2026' },
    nis: { label: 'NIS/PIS/PASEP', regex: '^\\d{3}-?\\d{2}-?\\d{4}$', example: '627-18-9432' },
    rg: { label: 'RG', regex: '^\\d{1,2}\\.?\\d{3}\\.?\\d{3}-?[\\dX]$', example: '52.234.567-X' },
};

interface TableCreatorDrawerProps {
    isOpen: boolean;
    onClose: () => void;
    tables: { name: string }[];
    schemas: string[];
    activeSchema: string;
    projectId: string;
    fetchWithAuth: (url: string, options?: any) => Promise<any>;
    onSqlGenerated: (sql: string, metaConfig: { tableName: string, mcpEnabled: boolean, mcpPerms: { r: boolean, c: boolean, u: boolean, d: boolean }, lockedColumns?: Record<string, string>, maskedColumns?: Record<string, string>, computedColumns?: Record<string, { formula: string, return_type?: string }>, autoClockColumns?: Record<string, { type: string }>, tableSecurity?: Record<string, TableSecurityConfig> }) => Promise<any> | any;
    onSqlSaveToEditor?: (sql: string) => void;
    initialTableName?: string;
    initialColumns?: ColumnDef[];
    initialRlsPolicies?: string[];
}

const ChallengeFactorOptions = [
    { value: 'otp', label: 'OTP' },
    { value: 'biometria', label: 'Passkey' },
    { value: 'totp', label: 'TOTP/MFA' }
];

// --- Helpers ---
const getUUID = () => {
    if (typeof crypto !== 'undefined' && crypto.randomUUID) {
        try { return crypto.randomUUID(); } catch (e) { /* ignore */ }
    }
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c: any) => {
        const r = (Math.random() * 16) | 0;
        const v = c === 'x' ? r : (r & 0x3) | 0x8;
        return v.toString(16);
    });
};

const sanitizeName = (val: string) =>
    val.toLowerCase().normalize("NFD").replace(/[\u0300-\u036f]/g, "").replace(/[^a-z0-9_]/g, "_").replace(/_+/g, "_").replace(/^[0-9]/, "_$&");

// Expanded default suggestions per type — PostgreSQL 18 native capabilities
const getDefaultSuggestions = (type: string, hasIdentity?: boolean): string[] => {
    // If identity is active, no default needed (they're mutually exclusive in PG)
    if (hasIdentity) return [];
    const t = type.toLowerCase();
    if (t === 'uuid') return ['gen_random_uuid()'];
    if (t.includes('timestamp') || t === 'date') return ['now()', 'current_timestamp', 'current_date', "timezone('utc', now())"];
    if (t === 'time') return ['current_time', 'localtime'];
    if (t === 'interval') return ["'1 hour'::interval", "'30 days'::interval", "'0'::interval"];
    if (t.includes('bool')) return ['true', 'false'];
    if (IDENTITY_COMPATIBLE_TYPES.has(t)) return ['0', '1'];
    if (t.includes('numeric') || t.includes('float') || t === 'float8' || t === 'money') return ['0', '1'];
    if (t.includes('json')) return ["'{}'::jsonb", "'[]'::jsonb", "'null'::jsonb"];
    if (t === 'text' || t === 'varchar') return ["''", 'current_user', 'session_user'];
    if (t === 'inet') return ["'0.0.0.0'::inet", 'inet_client_addr()'];
    if (t === 'point') return ["'(0,0)'::point"];
    if (t === 'bytea') return ["'\\x'::bytea"];
    return [];
};

// Smart quoting
const BARE_PATTERNS = [
    /^gen_random_uuid\(\)$/i,
    /^now\(\)$/i,
    /^current_(timestamp|date|time)$/i,
    /^localtime$/i,
    /^timezone\(/i,
    /^nextval\(/i,
    /^true$/i,
    /^false$/i,
    /^null$/i,
    /^current_user$/i,
    /^session_user$/i,
    /^inet_client_addr\(\)$/i,
];

const formatDefaultValue = (type: string, raw: string): string => {
    const v = raw.trim();
    if (!v) return '';
    if (v.startsWith("'") && v.endsWith("'")) return v;
    if (v.includes('::')) return v;
    if (BARE_PATTERNS.some(p => p.test(v))) return v;
    if (v.includes('(') && v.includes(')')) return v;
    const tn = type.toLowerCase();
    if (/^(int|float|numeric|real|double|serial|bigserial)/.test(tn) && !isNaN(Number(v))) return v;
    if (/bool/.test(tn) && ['true', 'false'].includes(v.toLowerCase())) return v;
    return `'${v.replace(/'/g, "''")}'`;
};

const DEFAULT_COLUMNS: ColumnDef[] = [
    { id: '1', name: 'id', type: 'uuid', defaultValue: 'gen_random_uuid()', isPrimaryKey: true, isNullable: false, isUnique: true, isArray: false, lockLevel: 'immutable', maskLevel: 'unmasked' },
    { id: '2', name: 'created_at', type: 'timestamptz', defaultValue: 'now()', isPrimaryKey: false, isNullable: false, isUnique: false, isArray: false, lockLevel: 'immutable', maskLevel: 'unmasked' },
    { id: '3', name: 'updated_at', type: 'timestamptz', defaultValue: 'now()', isPrimaryKey: false, isNullable: false, isUnique: false, isArray: false, lockLevel: 'auto_clock', maskLevel: 'unmasked' },
    { id: '4', name: '', type: 'text', defaultValue: '', isPrimaryKey: false, isNullable: true, isUnique: false, isArray: false },
];

const TableCreatorDrawer: React.FC<TableCreatorDrawerProps> = ({
    isOpen,
    onClose,
    tables,
    schemas,
    activeSchema,
    projectId,
    fetchWithAuth,
    onSqlGenerated,
    onSqlSaveToEditor,
    initialTableName = '',
    initialColumns,
    initialRlsPolicies,
}) => {
    const [tableName, setTableName] = useState(initialTableName);
    const [tableDesc, setTableDesc] = useState('');
    const [columns, setColumns] = useState<ColumnDef[]>(initialColumns || [...DEFAULT_COLUMNS]);
    const [enableRLS, setEnableRLS] = useState(true);
    const [enableRlsSuggestions, setEnableRlsSuggestions] = useState(false);
    const [activeFkEditor, setActiveFkEditor] = useState<string | null>(null);
    const [existingRlsPolicies, setExistingRlsPolicies] = useState<string[]>(initialRlsPolicies || []);
    
    // RLS Policy Suggestions
    const [rlsPolicies, setRlsPolicies] = useState<{
        select: string;
        insert: string;
        update: string;
        delete: string;
    }>({
        select: 'none',
        insert: 'none', 
        update: 'none',
        delete: 'none'
    });

    // Dynamic owner column selection
    const [selectedOwnerColumns, setSelectedOwnerColumns] = useState<string[]>([]);

    // Detect columns with FK to auth.users
    const getAuthUserColumns = () => {
        return columns.filter(c => 
            c.foreignKey?.schema === 'auth' && 
            c.foreignKey?.table === 'users'
        );
    };

    // Helper to generate dynamic preview for RLS clauses
    const getPolicyClausePreview = (operation: 'SELECT' | 'INSERT' | 'UPDATE' | 'DELETE', policyType: string) => {
        if (policyType === 'none') return operation === 'INSERT' ? 'WITH CHECK (false)' : 'USING (false)';
        if (policyType === 'authenticated' || policyType === 'public') return operation === 'INSERT' ? 'WITH CHECK (true)' : 'USING (true)';
        if (policyType === 'blocked') return '🚫 Service role only';
        
        if (policyType === 'owner_only') {
            // Effective owner columns: either specifically selected, or all autoUser columns if nothing selected
            const effectiveOwnerCols = columns.filter(c => 
                selectedOwnerColumns.includes(c.id) || 
                (selectedOwnerColumns.length === 0 && c.autoUser)
            );
            
            if (effectiveOwnerCols.length > 0) {
                const clauses = effectiveOwnerCols.map(c => `auth.uid() = ${sanitizeName(c.name || 'user_id')}`);
                const combined = clauses.length > 1 ? clauses.join(' OR ') : clauses[0];
                return operation === 'INSERT' ? `WITH CHECK (${combined})` : `USING (${combined})`;
            }
            
            // Fallback placeholder
            return operation === 'INSERT' ? 'WITH CHECK (auth.uid() = user_id)' : 'USING (auth.uid() = user_id)';
        }
        return '';
    };

    // Auto-detect table structure and suggest policies
    useEffect(() => {
        const hasAutoUser = columns.some(c => c.autoUser);
        const hasSensitiveColumns = columns.some(c => c.lockLevel || c.maskLevel);
        const authUserColumns = getAuthUserColumns();
        
        if (!enableRLS) {
            setRlsPolicies({
                select: 'none',
                insert: 'none',
                update: 'none',
                delete: 'none'
            });
            setSelectedOwnerColumns([]);
            return;
        }

        // Auto-select owner columns if available
        if (authUserColumns.length > 0 && selectedOwnerColumns.length === 0) {
            setSelectedOwnerColumns(authUserColumns.map(c => c.id));
        }

        if (hasAutoUser || authUserColumns.length > 0) {
            // Ownership-based policies
            setRlsPolicies({
                select: 'owner_only',
                insert: 'authenticated',
                update: 'owner_only',
                delete: 'owner_only'
            });
        } else if (hasSensitiveColumns) {
            // Read-only for sensitive data
            setRlsPolicies({
                select: 'authenticated',
                insert: 'blocked',
                update: 'blocked',
                delete: 'blocked'
            });
        } else {
            // Generic authenticated access
            setRlsPolicies({
                select: 'authenticated',
                insert: 'authenticated',
                update: 'authenticated',
                delete: 'authenticated'
            });
        }
    }, [columns, enableRLS]);
    const [expandedColumns, setExpandedColumns] = useState<Set<string>>(new Set()); // Todas as colunas iniciam colapsadas
    const [fkTargetColumns, setFkTargetColumns] = useState<string[]>([]);
    const [fkTargetTables, setFkTargetTables] = useState<{ name: string }[]>([]);
    const [fkLoading, setFkLoading] = useState(false);

    const scrollRef = useRef<HTMLDivElement>(null);
    const lastAddedIdRef = useRef<string | null>(null);
    const columnInputRefs = useRef<Map<string, HTMLInputElement>>(new Map());

    // Validation
    const hasEmptyColumn = columns.some(c => !c.name.trim());
    const canGenerate = !!tableName && !hasEmptyColumn;

    useEffect(() => {
        if (initialTableName) setTableName(initialTableName);
    }, [initialTableName]);

    useEffect(() => {
        if (initialColumns) setColumns(initialColumns);
    }, [initialColumns]);

    useEffect(() => {
        if (initialRlsPolicies) setExistingRlsPolicies(initialRlsPolicies);
    }, [initialRlsPolicies]);

    // MCP Access state
    const [mcpEnabled, setMcpEnabled] = useState(false);
    const [mcpPerms, setMcpPerms] = useState(() => {
        try {
            const saved = localStorage.getItem(`cascata_mcp_defaults_${projectId}`);
            if (saved) return JSON.parse(saved);
        } catch { /* ignore */ }
        return { r: true, c: true, u: true, d: false };
    });
    const [tableSecurityEnabled, setTableSecurityEnabled] = useState(false);
    const [tableSecurityOperations, setTableSecurityOperations] = useState<string[]>(['update']);
    const [tableSecurityFactors, setTableSecurityFactors] = useState<string[]>(['totp']);

    useEffect(() => {
        if (!isOpen) return;
        (async () => {
            try {
                const res = await fetchWithAuth(`/api/data/${projectId}/metadata`);
                const gov = res?.metadata?.ai_governance;
                setMcpEnabled(gov?.mcp_enabled === true);
            } catch { setMcpEnabled(false); }
        })();
    }, [isOpen, projectId]);

    // ENUM Types (PostgreSQL native enums)
    const [enumTypes, setEnumTypes] = useState<EnumType[]>([]);
    const [enumTypesLoading, setEnumTypesLoading] = useState(false);

    useEffect(() => {
        if (!isOpen) return;
        let cancelled = false;
        (async () => {
            setEnumTypesLoading(true);
            try {
                const res = await fetchWithAuth(`/api/data/${projectId}/enum-types`);
                const list = Array.isArray(res) ? (res as EnumType[]) : [];
                if (!cancelled) setEnumTypes(list);
            } catch {
                if (!cancelled) setEnumTypes([]);
            } finally {
                if (!cancelled) setEnumTypesLoading(false);
            }
        })();
        return () => { cancelled = true; };
    }, [isOpen, projectId, fetchWithAuth]);

    const enumTypeOptions = useMemo(() => {
        return [...enumTypes]
            .filter(e => e?.name && e?.schema)
            .sort((a, b) => {
                // Prefer active schema first, then schema/name alpha
                const aScore = a.schema === activeSchema ? 0 : 1;
                const bScore = b.schema === activeSchema ? 0 : 1;
                if (aScore !== bScore) return aScore - bScore;
                const s = a.schema.localeCompare(b.schema);
                if (s !== 0) return s;
                return a.name.localeCompare(b.name);
            })
            .map((e) => {
                const value = `${e.schema}.${e.name}`;
                const label = e.schema === activeSchema ? e.name : `${e.schema}.${e.name}`;
                return { value, label };
            });
    }, [enumTypes, activeSchema]);

    const parseEnumFqName = useCallback((t: string): { schema: string; name: string } | null => {
        const m = String(t || '').match(/^([a-zA-Z0-9_]+)\.([a-zA-Z0-9_]+)$/);
        if (!m) return null;
        return { schema: m[1], name: m[2] };
    }, []);

    const getDefaultSuggestionsForColumn = useCallback((type: string, hasIdentity: boolean, isArray: boolean): string[] => {
        // ENUM defaults: suggest known values (casted to enum type)
        const enumRef = parseEnumFqName(type);
        if (enumRef) {
            const enumType = enumTypes.find(e => e.schema === enumRef.schema && e.name === enumRef.name);
            if (!enumType) return [];
            return (enumType.values || []).map((v) => {
                const escaped = String(v).replace(/'/g, "''");
                const fq = `${enumRef.schema}.${enumRef.name}`;
                return isArray
                    ? `ARRAY['${escaped}']::${fq}[]`
                    : `'${escaped}'::${fq}`;
            });
        }

        return getDefaultSuggestions(type, hasIdentity);
    }, [enumTypes, parseEnumFqName]);

    useEffect(() => {
        localStorage.setItem(`cascata_mcp_defaults_${projectId}`, JSON.stringify(mcpPerms));
    }, [mcpPerms, projectId]);

    // Ref para input do table name
    const tableNameInputRef = useRef<HTMLInputElement>(null);

    // Reset when drawer opens fresh + foco no Table Name
    useEffect(() => {
        if (isOpen && !initialTableName && !initialColumns) {
            setTableName('');
            setTableDesc('');
            setColumns([...DEFAULT_COLUMNS]);
            setEnableRLS(true);
            setEnableRlsSuggestions(false);
            setTableSecurityEnabled(false);
            setTableSecurityOperations(['update']);
            setTableSecurityFactors(['totp']);
            setActiveFkEditor(null);
            setExpandedColumns(new Set(['4'])); // Coluna em branco (id:4) inicia expandida
            // Foco no campo Table Name após abrir
            setTimeout(() => {
                tableNameInputRef.current?.focus();
            }, 100);
        }
    }, [isOpen]);

    // Auto-focus newly added column input
    useEffect(() => {
        if (lastAddedIdRef.current) {
            const id = lastAddedIdRef.current;
            lastAddedIdRef.current = null;
            requestAnimationFrame(() => {
                const input = columnInputRefs.current.get(id);
                if (input) {
                    input.focus();
                    input.scrollIntoView({ behavior: 'smooth', block: 'center' });
                }
            });
        }
    }, [columns]);

    // --- Column Operations ---
    const handleAddColumn = () => {
        const newId = getUUID();
        lastAddedIdRef.current = newId;
        setColumns(prev => [...prev, {
            id: newId, name: '', type: 'text', defaultValue: '',
            isPrimaryKey: false, isNullable: true, isUnique: false, isArray: false
        }]);
        // Nova coluna sem nome inicia expandida para edição
        setExpandedColumns(prev => new Set(prev).add(newId));
    };

    const handleRemoveColumn = (id: string) => {
        setColumns(prev => prev.filter(c => c.id !== id));
        columnInputRefs.current.delete(id);
        // Remove do expanded também
        setExpandedColumns(prev => {
            const newSet = new Set(prev);
            newSet.delete(id);
            return newSet;
        });
    };

    const toggleColumnExpand = (id: string) => {
        setExpandedColumns(prev => {
            const newSet = new Set(prev);
            if (newSet.has(id)) {
                newSet.delete(id);
            } else {
                newSet.add(id);
            }
            return newSet;
        });
    };

    const handleColumnChange = (id: string, field: string, value: any) => {
        setColumns(prev => prev.map(c => {
            if (c.id !== id) return c;

            const updated = { ...c, [field]: value };

            // Auto User clicked
            if (field === 'autoUser' && value === true) {
                if (!c.name || c.name.trim() === '') {
                    updated.name = 'user_id';
                }
                updated.type = 'uuid';
                updated.isNullable = false;
            }

            // ── MUTUAL EXCLUSION: Array ↔ Foreign Key ↔ Identity ──
            // PostgreSQL constraints that are mutually exclusive:
            // - REFERENCES cannot be on array columns
            // - GENERATED AS IDENTITY cannot be on array columns
            // - SERIAL types cannot be array columns
            if (field === 'isArray' && value === true) {
                updated.foreignKey = undefined;
                updated.identityGeneration = undefined;
                if (activeFkEditor === id) setActiveFkEditor(null);
            }

            // Identity activated → clear default (mutually exclusive in PG)
            if (field === 'identityGeneration' && value) {
                updated.defaultValue = '';
                updated.isArray = false;
            }

            if (field === 'lockLevel' && (value === 'code_protected' || value === 'otp_protected') && (!c.allowedFactors || c.allowedFactors.length === 0)) {
                updated.allowedFactors = ['totp'];
            }

            return updated;
        }));

        if (field === 'autoUser' && value === true) {
            setEnableRlsSuggestions(true);
        }
    };

    const handleSetForeignKey = async (id: string, fkSchema: string, table: string, column: string) => {
        setColumns(prev => prev.map(c => {
            if (c.id !== id) return c;
            const updated = {
                ...c,
                foreignKey: table ? { schema: fkSchema || activeSchema, table, column: column || '' } : undefined,
                // ── MUTUAL EXCLUSION: FK activated → deactivate Array ──
                isArray: table ? false : c.isArray
            };
            return updated;
        }));

        if (table) {
            setFkLoading(true);
            try {
                const res = await fetchWithAuth(`/api/data/${projectId}/tables/${table}/columns?schema=${fkSchema || activeSchema}`);
                const cols = res.map((c: any) => c.name);
                setFkTargetColumns(cols);
                const defaultCol = cols.includes('id') ? 'id' : cols[0] || '';
                setColumns(prev => prev.map(c =>
                    c.id === id ? { ...c, foreignKey: { schema: fkSchema || activeSchema, table, column: defaultCol } } : c
                ));
            } catch (e) { /* ignore */ }
            finally { setFkLoading(false); }
        }
    };

    // Load tables for a specific FK schema
    const loadFkTablesForSchema = async (fkSchema: string) => {
        setFkLoading(true);
        try {
            const res = await fetchWithAuth(`/api/data/${projectId}/tables?schema=${fkSchema}`);
            setFkTargetTables(res || []);
        } catch { setFkTargetTables([]); }
        finally { setFkLoading(false); }
    };

    // Load tables for the current schema on FK editor open
    const handleOpenFkEditor = async (colId: string) => {
        if (activeFkEditor === colId) {
            setActiveFkEditor(null);
            return;
        }
        setActiveFkEditor(colId);
        // Default to current schema's tables
        const col = columns.find(c => c.id === colId);
        const fkSchema = col?.foreignKey?.schema || activeSchema;
        await loadFkTablesForSchema(fkSchema);
    };

    // --- Enterprise SQL Generator (pure text — no side effects) ---
    const generateSQLText = useCallback((): { sql: string; safeName: string; lockedColumns: Record<string, string>; maskedColumns: Record<string, string>; computedColumns: Record<string, { formula: string, return_type?: string, strict_mode?: boolean }>; autoClockColumns: Record<string, { type: string }>; tableSecurity: Record<string, TableSecurityConfig>; rlsPolicies: string[] } | null => {
        if (!canGenerate) return null;
        const safeName = sanitizeName(tableName);
        const schema = activeSchema || 'public';

        // Generate RLS Policies based on suggestions
        const generateRLSPolicies = () => {
            const policyLines: string[] = [];
            const authUserColumns = getAuthUserColumns();
            
            if (!enableRLS || !enableRlsSuggestions) {
                return [];
            }

            // Helper to generate policy name
            const getPolicyName = (operation: string, scope: string) => {
                return `${safeName}_${operation}_${scope}`;
            };

            // Helper to generate policy based on type
            const generatePolicy = (operation: string, scope: string, usingClause: string, withCheckClause?: string, customPolicyName?: string) => {
                const policyName = customPolicyName || getPolicyName(operation, scope);
                const role = scope === 'service_role' ? 'service_role, cascata_admin' : 'authenticated';
                
                let policy = `CREATE POLICY "${policyName}"\n`;
                policy += `ON ${schema}.${safeName}\n`;
                policy += `FOR ${operation}\n`;
                policy += `TO ${role}\n`;
                
                // SELECT, DELETE and UPDATE support USING
                if (['SELECT', 'DELETE', 'UPDATE', 'ALL'].includes(operation)) {
                    policy += `USING (${usingClause})`;
                }
                
                // INSERT and UPDATE support WITH CHECK
                if (['INSERT', 'UPDATE', 'ALL'].includes(operation)) {
                    const check = withCheckClause || usingClause;
                    if (operation !== 'INSERT') policy += '\n';
                    policy += `WITH CHECK (${check})`;
                }
                
                return policy + ';';
            };

            // Helper to generate owner policies (one per column)
            const generateOwnerPolicies = (operation: string, withCheck?: boolean) => {
                const policies: string[] = [];
                
                // Effective owner columns: either specifically selected, or all autoUser columns if nothing selected
                const effectiveOwnerCols = columns.filter(c => 
                    selectedOwnerColumns.includes(c.id) || 
                    (selectedOwnerColumns.length === 0 && c.autoUser)
                );

                if (effectiveOwnerCols.length > 0) {
                    effectiveOwnerCols.forEach(col => {
                        const colName = sanitizeName(col.name || 'user_id');
                        const clause = `auth.uid() = ${colName}`;
                        const policyName = `${safeName}_${operation}_${colName}`;
                        policies.push(generatePolicy(operation, 'authenticated', clause, withCheck ? clause : undefined, policyName));
                    });
                } else {
                    // Fallback to user_id if no owner columns defined (might fail, but consistent with selection)
                    const clause = 'auth.uid() = user_id';
                    policies.push(generatePolicy(operation, 'authenticated', clause, withCheck ? clause : undefined));
                }
                
                return policies;
            };

            // SELECT Policies
            if (rlsPolicies.select === 'owner_only') {
                policyLines.push(...generateOwnerPolicies('SELECT'));
            } else if (rlsPolicies.select === 'authenticated') {
                policyLines.push(generatePolicy('SELECT', 'authenticated', 'true'));
            } else if (rlsPolicies.select === 'public') {
                policyLines.push(generatePolicy('SELECT', 'public', 'true'));
            }

            // INSERT Policies
            if (rlsPolicies.insert === 'authenticated') {
                policyLines.push(...generateOwnerPolicies('INSERT', true));
            } else if (rlsPolicies.insert === 'blocked') {
                policyLines.push(generatePolicy('ALL', 'service_role', 'true'));
            }

            // UPDATE Policies
            if (rlsPolicies.update === 'owner_only') {
                policyLines.push(...generateOwnerPolicies('UPDATE', true));
            } else if (rlsPolicies.update === 'authenticated') {
                policyLines.push(generatePolicy('UPDATE', 'authenticated', 'true'));
            } else if (rlsPolicies.update === 'blocked') {
                policyLines.push(generatePolicy('ALL', 'service_role', 'true'));
            }

            // DELETE Policies
            if (rlsPolicies.delete === 'owner_only') {
                policyLines.push(...generateOwnerPolicies('DELETE'));
            } else if (rlsPolicies.delete === 'blocked') {
                policyLines.push(generatePolicy('ALL', 'service_role', 'true'));
            }

            return policyLines;
        };

        const colNames = columns.map(c => sanitizeName(c.name || 'unnamed'));
        const maxNameLen = Math.max(...colNames.map(n => n.length), 10);

        const colDefs = columns.map((c) => {
            const name = sanitizeName(c.name || 'unnamed');
            const paddedName = name.padEnd(maxNameLen);
            const type = c.isArray ? `${c.type}[]` : c.type;

            let constraints: string[] = [];

            if (c.isPrimaryKey) constraints.push('PRIMARY KEY');
            if (!c.isNullable && !c.isPrimaryKey) constraints.push('NOT NULL');
            if (c.isUnique && !c.isPrimaryKey) constraints.push('UNIQUE');

            // GENERATED AS IDENTITY (modern auto-increment) — mutually exclusive with DEFAULT
            if (c.identityGeneration && IDENTITY_COMPATIBLE_TYPES.has(c.type)) {
                const gen = c.identityGeneration === 'always' ? 'ALWAYS' : 'BY DEFAULT';
                constraints.push(`GENERATED ${gen} AS IDENTITY`);
            } else if (c.defaultValue && c.defaultValue.trim() && !SERIAL_TYPES.has(c.type)) {
                // Serial types auto-create sequences — no DEFAULT needed
                const formatted = formatDefaultValue(c.type, c.defaultValue);
                constraints.push(`DEFAULT ${formatted}`);
            }

            // Foreign key constraint — includes schema for cross-schema references
            if (c.foreignKey && c.foreignKey.table && c.foreignKey.column) {
                const fkSchema = c.foreignKey.schema || schema;
                const fkTable = sanitizeName(c.foreignKey.table);
                const fkCol = sanitizeName(c.foreignKey.column);
                constraints.push(`REFERENCES ${fkSchema}.${fkTable}(${fkCol})`);
            }

            // Auto User: DEFAULT auth.uid() REFERENCES auth.users(id)
            if (c.autoUser) {
                constraints.push('DEFAULT auth.uid()');
                // Avoid duplicate REFERENCES if manually set to auth.users
                const alreadyHasAuthFK = c.foreignKey && 
                                       (c.foreignKey.schema === 'auth' || (!c.foreignKey.schema && schema === 'auth')) && 
                                       c.foreignKey.table === 'users';
                if (!alreadyHasAuthFK) {
                    constraints.push('REFERENCES auth.users(id)');
                }
            }

            const constraintStr = constraints.length > 0 ? ' ' + constraints.join(' ') : '';
            return `    ${paddedName} ${type}${constraintStr}`;
        });

        const lines: string[] = [];
        lines.push(`-- Create table: ${safeName}`);
        lines.push(`CREATE TABLE IF NOT EXISTS ${schema}.${safeName} (`);
        lines.push(colDefs.join(',\n'));
        lines.push(`);`);

        if (enableRLS) {
            lines.push('');
            lines.push(`-- Enable Row Level Security`);
            lines.push(`ALTER TABLE ${schema}.${safeName} ENABLE ROW LEVEL SECURITY;`);

            // Add existing RLS Policies if present
            if (existingRlsPolicies.length > 0) {
                lines.push('');
                lines.push(`-- Row Level Security Policies (from SQL Console)`);
                existingRlsPolicies.forEach(policy => lines.push(policy));
            } else {
                // Add auto-generated RLS Policies
                const policies = generateRLSPolicies();
                if (policies.length > 0) {
                    lines.push('');
                    lines.push(`-- Row Level Security Policies`);
                    policies.forEach(policy => lines.push(policy));
                }
            }
        }

        // Column comments
        const commentLines: string[] = [];
        columns.forEach((c) => {
            const name = sanitizeName(c.name || 'unnamed');
            // Get the actual REGEX pattern - not just the preset name!
            let formatPattern: string | null = null;
            if (c.formatPreset && c.formatPreset !== 'custom') {
                formatPattern = FORMAT_PRESETS[c.formatPreset]?.regex || null;
            } else if (c.formatPreset === 'custom' && c.formatPattern) {
                formatPattern = c.formatPattern;
            }
            const desc = c.description || '';
            const commentBody = formatPattern ? `${desc}||FORMAT:${formatPattern}` : desc;
            if (commentBody) {
                commentLines.push(`COMMENT ON COLUMN ${schema}.${safeName}.${name} IS '${commentBody.replace(/'/g, "''")}';`);
            }
        });
        if (commentLines.length > 0) {
            lines.push('');
            lines.push('-- Column format validation & descriptions');
            commentLines.forEach(l => lines.push(l));
        }

        const hasDesc = tableDesc.trim().length > 0;
        if (hasDesc || mcpEnabled) {
            lines.push('');
            lines.push('-- Table Comment & Governance');
            const cleanDesc = hasDesc ? tableDesc.replace(/'/g, "''").trim() : '';
            if (mcpEnabled) {
                const mcpFlag = `MCP:${mcpPerms.r ? 'R' : ''}${mcpPerms.c ? 'C' : ''}${mcpPerms.u ? 'U' : ''}${mcpPerms.d ? 'D' : ''}`;
                lines.push(`COMMENT ON TABLE ${schema}.${safeName} IS '${cleanDesc}||${mcpFlag}';`);
            } else {
                lines.push(`COMMENT ON TABLE ${schema}.${safeName} IS '${cleanDesc}';`);
            }
        }

        // TIER-3 PADLOCK + MASKING + COMPUTED + AUTO CLOCK
        const lockedColumns: Record<string, any> = {};
        const maskedColumns: Record<string, string> = {};
        const computedColumns: Record<string, { formula: string, return_type?: string, strict_mode?: boolean }> = {};
        const autoClockColumns: Record<string, { type: string }> = {};
        columns.forEach(c => {
            const colName = sanitizeName(c.name || 'unnamed');
            // Auto Clock é tratado separadamente (não vai para lockedColumns)
            if (c.lockLevel === 'auto_clock') {
                autoClockColumns[colName] = { type: c.type };
            } else if (c.lockLevel && c.lockLevel !== 'unlocked') {
                if ((c.lockLevel === 'code_protected' || c.lockLevel === 'otp_protected') && c.allowedFactors && c.allowedFactors.length > 0) {
                    lockedColumns[colName] = {
                        lock_type: c.lockLevel,
                        metadata: { allowed_factors: c.allowedFactors },
                        allowed_factors: c.allowedFactors
                    };
                } else {
                    lockedColumns[colName] = c.lockLevel;
                }
            }
            if (c.maskLevel && c.maskLevel !== 'unmasked') {
                maskedColumns[colName] = c.maskLevel;
            }
            if (c.formula && c.formula.trim()) {
                computedColumns[colName] = {
                    formula: c.formula.trim(),
                    return_type: c.returnType,
                    strict_mode: c.strictMode
                };
            }
        });

        // DEBUG: Log computed columns
        console.log('[TableCreatorDrawer] Generated computedColumns:', computedColumns);
        console.log('[TableCreatorDrawer] All columns:', columns.map(c => ({ name: c.name, formula: c.formula, returnType: c.returnType })));

        const tableSecurity: Record<string, TableSecurityConfig> = {};
        if (tableSecurityEnabled && tableSecurityOperations.length > 0 && tableSecurityFactors.length > 0) {
            tableSecurity[safeName] = {
                operations: tableSecurityOperations,
                allowed_factors: tableSecurityFactors,
            };
        }

        return { sql: lines.join('\n'), safeName, lockedColumns, maskedColumns, computedColumns, autoClockColumns, tableSecurity, rlsPolicies: generateRLSPolicies() };
    }, [tableName, tableDesc, columns, enableRLS, enableRlsSuggestions, activeSchema, mcpEnabled, mcpPerms, rlsPolicies, canGenerate, selectedOwnerColumns, existingRlsPolicies, tableSecurityEnabled, tableSecurityOperations, tableSecurityFactors]);

    // --- Execute SQL (Generate + Fire callback + Close) ---
    const generateSQL = useCallback(async () => {
        const result = generateSQLText();
        if (!result) return;
        try {
            const res = await onSqlGenerated(result.sql, {
                tableName: result.safeName,
                mcpEnabled,
                mcpPerms,
                lockedColumns: result.lockedColumns,
                maskedColumns: result.maskedColumns,
                computedColumns: result.computedColumns,
                autoClockColumns: result.autoClockColumns,
                tableSecurity: result.tableSecurity
            });
            // If res is explicitly false, it means database execution failed.
            // In that case, we do not close the drawer so the owner doesn't lose their inputs.
            if (res !== false) {
                onClose();
            }
        } catch (err) {
            console.error('[TableCreatorDrawer] Error executing SQL:', err);
        }
    }, [generateSQLText, mcpEnabled, mcpPerms, onSqlGenerated, onClose]);

    // --- Save SQL to Editor (Generate + Send to console + Close) ---
    const saveToEditor = useCallback(() => {
        const result = generateSQLText();
        if (!result) return;
        if (onSqlSaveToEditor) onSqlSaveToEditor(result.sql);
        onClose();
    }, [generateSQLText, onSqlSaveToEditor, onClose]);

    // --- Keyboard Shortcuts (Ctrl+Enter = Execute, Ctrl+S = Save to Editor) ---
    useEffect(() => {
        if (!isOpen) return;
        const handler = (e: KeyboardEvent) => {
            // Ctrl+S → Save SQL to editor without executing
            if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                e.preventDefault();
                saveToEditor();
                return;
            }
            // Ctrl+Enter → Generate & Execute SQL
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                e.preventDefault();
                generateSQL();
                return;
            }
        };
        window.addEventListener('keydown', handler);
        return () => window.removeEventListener('keydown', handler);
    }, [isOpen, generateSQL, saveToEditor]);

    // Click outside handler for FK editor
    useEffect(() => {
        if (!activeFkEditor) return;
        const handler = (e: MouseEvent) => {
            const target = e.target as HTMLElement;
            if (!target.closest('[data-fk-editor]')) {
                setActiveFkEditor(null);
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, [activeFkEditor]);

    return (
        <div className={`fixed inset-y-0 right-0 w-[690px] bg-white shadow-2xl z-[100] transform transition-transform duration-300 ease-in-out flex flex-col border-l border-slate-200 ${isOpen ? 'translate-x-0' : 'translate-x-full'}`}>
            {/* Header */}
            <div className="p-6 border-b border-slate-100 flex items-center justify-between bg-slate-50/50">
                <div>
                    <h3 className="text-xl font-black text-slate-900 tracking-tight">Create New Table</h3>
                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-1">Schema Designer</p>
                </div>
                <button onClick={onClose} className="p-2 hover:bg-slate-200 rounded-lg text-slate-400">
                    <X size={20} />
                </button>
            </div>

            {/* Content */}
            <div className="flex-1 overflow-y-auto p-6 space-y-8" ref={scrollRef}>
                {/* Table Name + Description */}
                <div className="space-y-4">
                    <div className="space-y-2">
                        <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Table Name</label>
                        <input
                            ref={tableNameInputRef}
                            value={tableName}
                            onChange={(e: any) => setTableName(sanitizeName(e.target.value))}
                            onKeyDown={(e: any) => {
                                if (e.key === 'Enter') {
                                    e.preventDefault();
                                    // Procura coluna com nome vazio
                                    const emptyCol = columns.find(c => !c.name.trim());
                                    if (emptyCol) {
                                        // Foca na coluna vazia existente
                                        const input = columnInputRefs.current.get(emptyCol.id);
                                        if (input) {
                                            input.focus();
                                            input.scrollIntoView({ behavior: 'smooth', block: 'center' });
                                        }
                                    } else {
                                        // Cria nova coluna e foca nela
                                        handleAddColumn();
                                    }
                                }
                            }}
                            placeholder="users"
                            className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold outline-none focus:border-slate-800/50 focus:ring-2 focus:ring-slate-800/10"
                        />
                    </div>
                    <div className="space-y-2">
                        <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Description (for AI)</label>
                        <div className="flex gap-2">
                            <input
                                value={tableDesc}
                                onChange={(e: any) => setTableDesc(e.target.value)}
                                placeholder="e.g. Stores registered users."
                                className="flex-1 bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-medium outline-none focus:ring-2 focus:ring-indigo-500/20 text-slate-600"
                            />
                            <button
                                onClick={() => { /* TODO: AI schema generation */ }}
                                className="px-4 py-2 bg-indigo-600 hover:bg-indigo-700 text-white rounded-xl text-xs font-bold transition-colors flex items-center gap-2"
                                title="Generate schema with AI (coming soon)"
                            >
                                <Sparkles size={14} />
                                AI
                            </button>
                        </div>
                    </div>
                </div>

                {/* Column Definitions */}
                <div className="space-y-4">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Column Definitions</label>
                    <div className="space-y-3">
                        {columns.map((col) => {
                            const isExpanded = expandedColumns.has(col.id);
                            const hasAdvancedConfig = col.defaultValue || col.isPrimaryKey || col.identityGeneration || col.foreignKey ||
                                col.isArray || !col.isNullable || col.isUnique || col.formatPreset || col.formatPattern ||
                                col.formula || (col.lockLevel && col.lockLevel !== 'unlocked') ||
                                (col.maskLevel && col.maskLevel !== 'unmasked');
                            return (
                            <div
                                key={col.id}
                                onClick={() => toggleColumnExpand(col.id)}
                                className={`bg-white border rounded-xl p-3 shadow-sm hover:shadow-md transition-all group relative cursor-pointer ${!col.name.trim() ? 'border-slate-200' : 'border-slate-200'}`}
                            >
                                <div className="flex gap-3 items-center">
                                    <input
                                        ref={(el) => { if (el) columnInputRefs.current.set(col.id, el); }}
                                        value={col.name}
                                        onClick={(e: any) => e.stopPropagation()}
                                        onChange={(e: any) => handleColumnChange(col.id, 'name', sanitizeName(e.target.value))}
                                        onKeyDown={(e: any) => { if (e.key === 'Enter') { e.preventDefault(); handleAddColumn(); } }}
                                        placeholder="column_name"
                                        className={`flex-[2] bg-slate-50 rounded-lg px-3 py-2 text-xs font-bold outline-none ${!col.name.trim() ? 'border-none placeholder:text-slate-500' : 'border-none'}`}
                                    />
                                    <select
                                        value={col.type}
                                        onClick={(e: any) => e.stopPropagation()}
                                        onChange={(e: any) => {
                                            const newType = e.target.value;
                                            handleColumnChange(col.id, 'type', newType);
                                            // AUTO-CLEAR identity if new type doesn't support it
                                            if (!IDENTITY_COMPATIBLE_TYPES.has(newType) && col.identityGeneration) {
                                                handleColumnChange(col.id, 'identityGeneration', undefined);
                                            }
                                            // AUTO-CLEAR identity if switching to serial (has built-in auto-inc)
                                            if (SERIAL_TYPES.has(newType) && col.identityGeneration) {
                                                handleColumnChange(col.id, 'identityGeneration', undefined);
                                            }
                                        }}
                                        className="flex-1 bg-slate-100 border-none rounded-lg px-2 py-2 text-[10px] font-black uppercase text-slate-600 outline-none cursor-pointer"
                                    >
                                            <optgroup label="Numbers">
                                                <option value="int8">int8 (BigInt)</option>
                                                <option value="int4">int4 (Integer)</option>
                                                <option value="int2">int2 (SmallInt)</option>
                                                <option value="numeric">numeric</option>
                                                <option value="float8">float8</option>
                                                <option value="money">money</option>
                                            </optgroup>
                                            <optgroup label="Text">
                                                <option value="text">text</option>
                                                <option value="varchar">varchar</option>
                                                <option value="uuid">uuid</option>
                                                <option value="citext">citext (Case-Insensitive)</option>
                                            </optgroup>
                                            <optgroup label="Date/Time">
                                                <option value="timestamptz">timestamptz</option>
                                                <option value="timestamp">timestamp</option>
                                                <option value="date">date</option>
                                                <option value="time">time</option>
                                                <option value="timetz">timetz</option>
                                                <option value="interval">interval</option>
                                            </optgroup>
                                            <optgroup label="Ranges">
                                                <option value="tstzrange">tstzrange (Timestamp Range)</option>
                                                <option value="daterange">daterange (Date Range)</option>
                                            </optgroup>
                                            <optgroup label="JSON">
                                                <option value="jsonb">jsonb</option>
                                                <option value="json">json</option>
                                            </optgroup>
                                            <optgroup label="Full-Text Search">
                                                <option value="tsvector">tsvector</option>
                                                <option value="tsquery">tsquery</option>
                                            </optgroup>
                                            <optgroup label="Network & Geo">
                                                <option value="inet">inet (IP Address)</option>
                                                <option value="macaddr">macaddr (MAC Address)</option>
                                                <option value="point">point (2D Coord)</option>
                                            </optgroup>
                                            <optgroup label="Hierarchy">
                                                <option value="ltree">ltree (Tree Path)</option>
                                            </optgroup>
                                            <optgroup label="Other">
                                                <option value="bool">boolean</option>
                                                <option value="bytea">bytea</option>
                                                <option value="xml">xml</option>
                                                <option value="vector">vector (Embedding)</option>
                                            </optgroup>
                                            <optgroup label="Auto-Increment (Legacy)">
                                                <option value="serial">serial (Auto Int4)</option>
                                                <option value="bigserial">bigserial (Auto Int8)</option>
                                            </optgroup>
                                            {/* ENUM types (dinâmicos do PostgreSQL) */}
                                            {(enumTypesLoading || enumTypeOptions.length > 0) && (
                                                <optgroup label="ENUMS">
                                                    {enumTypesLoading && (
                                                        <option value={col.type} disabled>
                                                            Loading enums...
                                                        </option>
                                                    )}
                                                    {enumTypeOptions.map((e) => (
                                                        <option key={e.value} value={e.value}>
                                                            {e.label}
                                                        </option>
                                                    ))}
                                                </optgroup>
                                            )}
                                    </select>
                                    {/* Indicador de configurações avançadas */}
                                    {hasAdvancedConfig && !isExpanded && (
                                        <div className="flex items-center gap-1 px-2 py-1 bg-indigo-50 rounded-lg" title="Coluna possui configurações avançadas">
                                            {col.lockLevel && col.lockLevel !== 'unlocked' && <Lock size={10} className="text-indigo-500" />}
                                            {col.maskLevel && col.maskLevel !== 'unmasked' && <EyeOff size={10} className="text-indigo-500" />}
                                            {col.formula && <Calculator size={10} className="text-emerald-500" />}
                                            {col.foreignKey && <LinkIcon size={10} className="text-blue-500" />}
                                            {col.isPrimaryKey && <span className="text-[8px] font-black text-amber-600">PK</span>}
                                            {!col.isNullable && <span className="text-[8px] font-black text-rose-500">!</span>}
                                        </div>
                                    )}
                                    <button
                                        onClick={(e: any) => { e.stopPropagation(); toggleColumnExpand(col.id); }}
                                        className="p-2 text-slate-400 hover:text-indigo-600 hover:bg-indigo-50 rounded-lg transition-colors"
                                        title={isExpanded ? "Recolher configurações" : "Expandir configurações"}
                                    >
                                        {isExpanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                                    </button>
                                    <button onClick={(e: any) => { e.stopPropagation(); handleRemoveColumn(col.id); }} className="p-2 text-slate-300 hover:text-rose-500 hover:bg-rose-50 rounded-lg transition-colors"><X size={14} /></button>
                                </div>

                                {/* Default Value + Constraint Toggles - apenas quando expandido */}
                                {isExpanded && (
                                <div onClick={(e: any) => e.stopPropagation()} className="flex items-center gap-3 bg-slate-50 p-2 rounded-lg relative mt-3">
                                    {col.identityGeneration ? (
                                        <span className="flex-1 text-[10px] font-mono text-teal-600 font-bold select-none" title="Default value is disabled when Identity (auto-increment) is active">
                                            ⚡ GENERATED {col.identityGeneration === 'always' ? 'ALWAYS' : 'BY DEFAULT'} AS IDENTITY
                                        </span>
                                    ) : SERIAL_TYPES.has(col.type) ? (
                                        <span className="flex-1 text-[10px] font-mono text-amber-600 font-bold select-none" title="Serial types have built-in auto-increment sequence">
                                            ⚡ AUTO-INCREMENT (sequence)
                                        </span>
                                    ) : (
                                        <>
                                            <input
                                                list={`defaults-${col.id}`}
                                                value={col.defaultValue}
                                                onClick={(e: any) => e.stopPropagation()}
                                                onChange={(e: any) => handleColumnChange(col.id, 'defaultValue', e.target.value)}
                                                placeholder="Default Value (NULL)"
                                                className="flex-1 bg-transparent border-none text-[10px] font-mono text-slate-600 outline-none placeholder:text-slate-300"
                                            />
                                            <datalist id={`defaults-${col.id}`}>
                                                {getDefaultSuggestionsForColumn(col.type, !!col.identityGeneration, !!col.isArray).map(s => <option key={s} value={s} />)}
                                            </datalist>
                                        </>
                                    )}
                                    <div className="h-4 w-[1px] bg-slate-200"></div>
                                    <div className="flex items-center gap-2">
                                        <div title="Primary Key" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'isPrimaryKey', !col.isPrimaryKey); }} className={`px-1.5 py-1 rounded text-[9px] font-black cursor-pointer select-none transition-colors ${col.isPrimaryKey ? 'bg-amber-100 text-amber-700' : 'text-slate-300 hover:bg-slate-200'}`}>PK</div>
                                        {/* AUTO — Identity (Auto-Increment) — only for int2/int4/int8, not serial/bigserial */}
                                        {IDENTITY_COMPATIBLE_TYPES.has(col.type) && !SERIAL_TYPES.has(col.type) && (
                                            <div
                                                title={col.isArray
                                                    ? "Auto-increment (disabled — Array columns cannot use IDENTITY)"
                                                    : col.identityGeneration
                                                        ? `Auto-increment: GENERATED ${col.identityGeneration === 'always' ? 'ALWAYS' : 'BY DEFAULT'} AS IDENTITY (click to cycle/disable)`
                                                        : "Auto-increment (GENERATED AS IDENTITY)"
                                                }
                                                onClick={(e: any) => {
                                                    e.stopPropagation();
                                                    if (col.isArray) return; // Identity incompatible with arrays
                                                    // Cycle: off → always → by_default → off
                                                    if (!col.identityGeneration) {
                                                        handleColumnChange(col.id, 'identityGeneration', 'always');
                                                        handleColumnChange(col.id, 'defaultValue', ''); // Clear default (mutually exclusive)
                                                    } else if (col.identityGeneration === 'always') {
                                                        handleColumnChange(col.id, 'identityGeneration', 'by_default');
                                                    } else {
                                                        handleColumnChange(col.id, 'identityGeneration', undefined);
                                                    }
                                                }}
                                                className={`px-1.5 py-1 rounded text-[9px] font-black cursor-pointer select-none transition-colors ${col.isArray ? 'text-slate-200 cursor-not-allowed' :
                                                    col.identityGeneration === 'always' ? 'bg-teal-100 text-teal-700 ring-1 ring-teal-300' :
                                                        col.identityGeneration === 'by_default' ? 'bg-cyan-100 text-cyan-700 ring-1 ring-cyan-300' :
                                                            'text-slate-300 hover:bg-slate-200'
                                                    }`}
                                            >
                                                AUTO
                                            </div>
                                        )}
                                        <div
                                            title={col.isArray ? "Foreign Key (disabled — Array columns cannot have REFERENCES)" : "Foreign Key"}
                                            onClick={(e: any) => {
                                                e.stopPropagation();
                                                if (col.isArray) return;
                                                handleOpenFkEditor(col.id);
                                            }}
                                            className={`px-1.5 py-1 rounded cursor-pointer select-none transition-colors flex items-center ${col.isArray ? 'text-slate-200 cursor-not-allowed' : col.foreignKey ? 'bg-blue-100 text-blue-700' : 'text-slate-300 hover:bg-slate-200'}`}
                                        >
                                            <LinkIcon size={12} strokeWidth={4} />
                                        </div>
                                        <div
                                            title={
                                                col.foreignKey ? "Array (disabled — FK columns cannot be arrays)" :
                                                    col.identityGeneration ? "Array (disabled — Identity columns cannot be arrays)" :
                                                        SERIAL_TYPES.has(col.type) ? "Array (disabled — Serial types cannot be arrays)" :
                                                            "Array"
                                            }
                                            onClick={(e: any) => {
                                                e.stopPropagation();
                                                if (col.foreignKey || col.identityGeneration || SERIAL_TYPES.has(col.type)) return;
                                                handleColumnChange(col.id, 'isArray', !col.isArray);
                                            }}
                                            className={`px-1.5 py-1 rounded text-[9px] font-black cursor-pointer select-none transition-colors ${(col.foreignKey || col.identityGeneration || SERIAL_TYPES.has(col.type))
                                                ? 'text-slate-200 cursor-not-allowed'
                                                : col.isArray ? 'bg-indigo-100 text-indigo-700' : 'text-slate-300 hover:bg-slate-200'
                                                }`}
                                        >
                                            LIST
                                        </div>
                                        <div title="Nullable" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'isNullable', !col.isNullable); }} className={`px-1.5 py-1 rounded text-[9px] font-black cursor-pointer select-none transition-colors ${col.isNullable ? 'bg-emerald-100 text-emerald-700' : 'text-slate-300 hover:bg-slate-200'}`}>NULL</div>
                                        <div title="Unique" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'isUnique', !col.isUnique); }} className={`px-1.5 py-1 rounded text-[9px] font-black cursor-pointer select-none transition-colors ${col.isUnique ? 'bg-purple-100 text-purple-700' : 'text-slate-300 hover:bg-slate-200'}`}>UNIQ</div>
                                        {(col.type === 'text' || col.type === 'varchar') && (
                                            <div title="Format Validation" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'formatPreset', col.formatPreset ? undefined : 'email'); }} className={`px-1.5 py-1 rounded cursor-pointer select-none transition-colors flex items-center gap-0.5 ${col.formatPreset || col.formatPattern ? 'bg-amber-100 text-amber-700' : 'text-slate-300 hover:bg-slate-200'}`}><Regex size={10} strokeWidth={3} /></div>
                                        )}
                                        <div title="Smart Formula (Computed Column)" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'formula', col.formula ? '' : '{{field}} * 2'); }} className={`px-1.5 py-1 rounded cursor-pointer select-none transition-colors flex items-center gap-0.5 ${col.formula ? 'bg-emerald-100 text-emerald-700 border border-emerald-300' : 'text-slate-300 hover:bg-slate-200'}`}><Calculator size={10} strokeWidth={3} /></div>
                                        <div title="Security Lock (Immutability)" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'lockLevel', col.lockLevel && col.lockLevel !== 'unlocked' ? 'unlocked' : 'immutable'); }} className={`px-1.5 py-1 rounded cursor-pointer select-none transition-colors flex items-center gap-0.5 ${col.lockLevel && col.lockLevel !== 'unlocked' ? 'bg-rose-100 text-rose-700 border border-rose-300' : 'text-slate-300 hover:bg-slate-200'}`}><Lock size={10} strokeWidth={3} /></div>
                                        <div title="Data Privacy (Read Masking)" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'maskLevel', col.maskLevel && col.maskLevel !== 'unmasked' ? 'unmasked' : 'hide'); }} className={`px-1.5 py-1 rounded cursor-pointer select-none transition-colors flex items-center gap-0.5 ${col.maskLevel && col.maskLevel !== 'unmasked' ? 'bg-indigo-100 text-indigo-700 border border-indigo-300' : 'text-slate-300 hover:bg-slate-200'}`}><EyeOff size={10} strokeWidth={3} /></div>
                                        <div title="Auto User (auth.uid())" onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'autoUser', !col.autoUser); }} className={`px-1.5 py-1 rounded cursor-pointer select-none transition-colors flex items-center gap-0.5 ${col.autoUser ? 'bg-blue-100 text-blue-700 border border-blue-300' : 'text-slate-300 hover:bg-slate-200'}`}><User size={10} strokeWidth={3} /></div>
                                    </div>
                                </div>
                                )}

                                {/* Format Validation Editor (inline) - apenas quando expandido */}
                                {isExpanded && (col.formatPreset || col.formatPattern) && (col.type === 'text' || col.type === 'varchar') && (
                                    <div onClick={(e: any) => e.stopPropagation()} className="mt-2 bg-amber-50/50 border border-amber-100 rounded-lg p-2 animate-in slide-in-from-top-1">
                                        <select
                                            value={col.formatPreset || 'custom'}
                                            onClick={(e: any) => e.stopPropagation()}
                                            onChange={(e) => {
                                                const val = e.target.value;
                                                if (val === 'custom') {
                                                    handleColumnChange(col.id, 'formatPreset', 'custom');
                                                    handleColumnChange(col.id, 'formatPattern', '');
                                                } else if (val === '') {
                                                    handleColumnChange(col.id, 'formatPreset', undefined);
                                                    handleColumnChange(col.id, 'formatPattern', undefined);
                                                } else {
                                                    handleColumnChange(col.id, 'formatPreset', val);
                                                    handleColumnChange(col.id, 'formatPattern', undefined);
                                                }
                                            }}
                                            className="w-full bg-white border border-amber-200 rounded py-1.5 px-2 text-[10px] font-bold text-slate-700 outline-none cursor-pointer"
                                        >
                                            <option value="">Remove Format</option>
                                            {Object.entries(FORMAT_PRESETS).map(([key, p]) => (
                                                <option key={key} value={key}>{p.label} ({p.example})</option>
                                            ))}
                                            <option value="custom">Custom Regex...</option>
                                        </select>
                                        {col.formatPreset === 'custom' && (
                                            <input
                                                value={col.formatPattern || ''}
                                                onClick={(e: any) => e.stopPropagation()}
                                                onChange={(e) => handleColumnChange(col.id, 'formatPattern', e.target.value)}
                                                placeholder="^[A-Z]{2}\d{4}$"
                                                className="w-full mt-1.5 bg-white border border-amber-200 rounded py-1.5 px-2 text-[10px] font-mono text-slate-600 outline-none"
                                            />
                                        )}
                                    </div>
                                )}

                                {/* Universal Security Lock Editor (inline) - apenas quando expandido */}
                                {isExpanded && col.lockLevel && col.lockLevel !== 'unlocked' && (
                                    <div onClick={(e: any) => e.stopPropagation()} className="mt-2 bg-rose-50/50 border border-rose-200 rounded-lg p-2 animate-in slide-in-from-top-1">
                                        <div className="flex items-center gap-2 mb-2">
                                            <Lock size={12} className="text-rose-500" />
                                            <span className="text-[10px] font-black text-rose-600 uppercase tracking-widest">Universal Security Lock</span>
                                        </div>
                                        <p className="text-[9px] text-rose-400 mb-3 font-medium leading-tight px-0.5">
                                            Prevents unauthorized API mutations based on the selected security tier.
                                        </p>
                                        <select
                                            value={col.lockLevel}
                                            onClick={(e: any) => e.stopPropagation()}
                                            onChange={(e) => handleColumnChange(col.id, 'lockLevel', e.target.value)}
                                            className="w-full bg-white border border-rose-200 rounded py-1.5 px-2 text-[10px] font-bold text-slate-700 outline-none cursor-pointer"
                                        >
                                            <option value="immutable">IMMUTABLE (API Blocks both INSERT & UPDATE)</option>
                                            <option value="insert_only">INSERT ONLY (API Blocks UPDATE)</option>
                                            <option value="service_role_only">SERVICE ROLE ONLY (API Blocks Anon & Authenticated users)</option>
                                            <option value="code_protected">CODE PROTECTED (API Blocks UPDATE unless step-up challenge is provided)</option>
                                            <option value="auto_clock">AUTO CLOCK (Auto-update on any column change - System Controlled)</option>
                                        </select>
                                        
                                        {(col.lockLevel === 'code_protected' || col.lockLevel === 'otp_protected') && (
                                            <div className="mt-3 p-2 bg-white/50 rounded border border-rose-100">
                                                <label className="text-[9px] font-black text-rose-500 uppercase tracking-widest block mb-2">
                                                    Quais fatores de segurança autorizam a alteração desta coluna?
                                                </label>
                                                <div className="flex flex-wrap gap-2">
                                                    {ChallengeFactorOptions.map(factor => {
                                                        const isSelected = (col.allowedFactors || []).includes(factor.value);
                                                        return (
                                                            <label key={factor.value} className="flex items-center gap-1 cursor-pointer">
                                                                <input
                                                                    type="checkbox"
                                                                    checked={isSelected}
                                                                    onChange={(e) => {
                                                                        const current = col.allowedFactors || [];
                                                                        const next = e.target.checked
                                                                            ? [...current, factor.value]
                                                                            : current.filter(f => f !== factor.value);
                                                                        handleColumnChange(col.id, 'allowedFactors', next);
                                                                    }}
                                                                    className="text-rose-500 rounded-sm outline-none"
                                                                />
                                                                <span className="text-[10px] font-bold text-slate-600">{factor.label}</span>
                                                            </label>
                                                        );
                                                    })}
                                                </div>
                                                <p className="mt-2 text-[9px] text-slate-400 font-mono">
                                                    metadata.allowed_factors = [{(col.allowedFactors || []).join(', ') || 'totp'}]
                                                </p>
                                            </div>
                                        )}
                                    </div>
                                )}

                                {/* Privacy Mask Editor (inline) - apenas quando expandido */}
                                {isExpanded && col.maskLevel && col.maskLevel !== 'unmasked' && (
                                    <div onClick={(e: any) => e.stopPropagation()} className="mt-2 bg-indigo-50/50 border border-indigo-200 rounded-lg p-2 animate-in slide-in-from-top-1">
                                        <div className="flex items-center gap-2 mb-2">
                                            <EyeOff size={12} className="text-indigo-500" />
                                            <span className="text-[10px] font-black text-indigo-600 uppercase tracking-widest">Data Privacy (Read/Write Masking)</span>
                                        </div>
                                        <p className="text-[9px] text-indigo-400 mb-3 font-medium leading-tight px-0.5">
                                            Controls how data is structurally modified before leaving the API layer.
                                        </p>
                                        <select
                                            value={col.maskLevel}
                                            onClick={(e: any) => e.stopPropagation()}
                                            onChange={(e) => handleColumnChange(col.id, 'maskLevel', e.target.value)}
                                            className="w-full bg-white border border-indigo-200 rounded py-1.5 px-2 text-[10px] font-bold text-slate-700 outline-none cursor-pointer"
                                        >
                                            <option value="hide">HIDE (Removed entirely from API outputs)</option>
                                            <option value="blur">BLUR (Shows only first and last characters)</option>
                                            <option value="mask">MASK (Replaced completley with '*' placeholder)</option>
                                            <option value="semi-mask">SEMI-MASK (75% Proportional Masking)</option>
                                            <option value="encrypt">ENCRYPT (Node.js AES-256 written ciphered to db)</option>
                                        </select>
                                    </div>
                                )}

                                {/* Formula Editor (inline) - visível quando houver fórmula */}
                                {col.formula && (
                                    <div onClick={(e: any) => e.stopPropagation()} className="mt-2 bg-emerald-50/50 border border-emerald-200 rounded-lg p-2 animate-in slide-in-from-top-1">
                                        <div className="flex items-center gap-2 mb-2">
                                            <Calculator size={12} className="text-emerald-500" />
                                            <span className="text-[10px] font-black text-emerald-600 uppercase tracking-widest">Smart Formula (Computed Column)</span>
                                        </div>
                                        <p className="text-[9px] text-emerald-500 mb-2 font-medium leading-tight px-0.5">
                                            Auto-calculates on insert/update. Use {'{{field_name}}'} for variables.
                                        </p>
                                        <input
                                            value={col.formula || ''}
                                            onClick={(e: any) => e.stopPropagation()}
                                            onChange={(e) => handleColumnChange(col.id, 'formula', e.target.value)}
                                            placeholder="{{price}} * {{qty}} * 1.18"
                                            className="w-full bg-white border border-emerald-200 rounded py-1.5 px-2 text-[10px] font-mono text-slate-700 outline-none"
                                        />
                                        <div className="mt-2 flex items-center gap-2">
                                            <label className="text-[8px] text-emerald-600 font-bold">PG Type:</label>
                                            <select
                                                value={col.returnType || 'float'}
                                                onClick={(e: any) => e.stopPropagation()}
                                                onChange={(e) => handleColumnChange(col.id, 'returnType', e.target.value)}
                                                className="flex-1 bg-white border border-emerald-200 rounded py-1 px-2 text-[9px] font-bold text-slate-700 outline-none cursor-pointer"
                                            >
                                                <option value="text">text</option>
                                                <option value="int">int</option>
                                                <option value="float">float</option>
                                                <option value="numeric">numeric</option>
                                                <option value="money">money</option>
                                                <option value="boolean">boolean</option>
                                                <option value="timestamp">timestamp</option>
                                                <option value="date">date</option>
                                            </select>
                                        </div>
                                        {/* Strict Mode Toggle */}
                                        <div className="mt-2 flex items-center justify-between">
                                            <div className="flex items-center gap-1.5">
                                                <span className={`text-[9px] font-bold ${col.strictMode ? 'text-rose-600' : 'text-emerald-600'}`}>
                                                    {col.strictMode ? '⚠ STRICT' : '✓ PERMISSIVE'}
                                                </span>
                                                <span className="text-[8px] text-slate-400">
                                                    {col.strictMode ? 'Errors cancel operation' : 'Errors = NULL'}
                                                </span>
                                            </div>
                                            <button
                                                onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'strictMode', !col.strictMode); }}
                                                className={`w-10 h-5 rounded-full p-0.5 transition-colors ${col.strictMode ? 'bg-rose-500' : 'bg-emerald-500'}`}
                                                title={col.strictMode ? 'Strict: Formula errors fail INSERT/UPDATE' : 'Permissive: Formula errors result in NULL'}
                                            >
                                                <div className={`w-4 h-4 bg-white rounded-full shadow-sm transition-transform ${col.strictMode ? 'translate-x-5' : 'translate-x-0'}`}></div>
                                            </button>
                                        </div>
                                        <div className="mt-2 flex flex-wrap gap-1">
                                            <span className="text-[8px] text-emerald-500 font-bold">FUNÇÕES:</span>
                                            {['SUM', 'UPPER', 'LOWER', 'ROUND', 'ABS', 'CONCAT'].map(fn => (
                                                <button
                                                    key={fn}
                                                    onClick={(e: any) => { e.stopPropagation(); handleColumnChange(col.id, 'formula', col.formula + fn + '()'); }}
                                                    className="px-1.5 py-0.5 bg-emerald-100 text-emerald-700 rounded text-[8px] font-bold hover:bg-emerald-200"
                                                >
                                                    {fn}()
                                                </button>
                                            ))}
                                        </div>
                                    </div>
                                )}

                                {/* FK Editor Popover — with Schema Selector - apenas quando expandido */}
                                {isExpanded && activeFkEditor === col.id && (
                                    <div data-fk-editor onClick={(e: any) => e.stopPropagation()} className="absolute z-50 top-full right-0 mt-2 w-72 bg-white border border-slate-200 shadow-xl rounded-xl p-4 animate-in fade-in zoom-in-95">
                                        <h4 className="text-[10px] font-black text-slate-400 uppercase tracking-widest mb-3">Link to Table</h4>
                                        <div className="space-y-3">
                                            {/* Schema Selector */}
                                            <div className="space-y-1">
                                                <label className="text-[9px] font-black text-blue-400 uppercase tracking-widest ml-0.5">Schema</label>
                                                <select
                                                    value={col.foreignKey?.schema || activeSchema}
                                                    onChange={async (e: any) => {
                                                        const newSchema = e.target.value;
                                                        // Update FK schema and clear table/column
                                                        setColumns(prev => prev.map(c =>
                                                            c.id === col.id ? { ...c, foreignKey: { schema: newSchema, table: '', column: '' }, isArray: false } : c
                                                        ));
                                                        // Load tables for the selected schema
                                                        await loadFkTablesForSchema(newSchema);
                                                        setFkTargetColumns([]);
                                                    }}
                                                    className="w-full bg-slate-50 border border-slate-200 rounded-lg py-2 px-3 text-xs font-bold text-slate-700 outline-none"
                                                >
                                                    {schemas.map(s => (
                                                        <option key={s} value={s}>{s}</option>
                                                    ))}
                                                </select>
                                            </div>

                                            {/* Table Selector */}
                                            <div className="space-y-1">
                                                <label className="text-[9px] font-black text-blue-400 uppercase tracking-widest ml-0.5">Table</label>
                                                <select
                                                    value={col.foreignKey?.table || ''}
                                                    onChange={(e: any) => {
                                                        const fkSchema = col.foreignKey?.schema || activeSchema;
                                                        handleSetForeignKey(col.id, fkSchema, e.target.value, '');
                                                    }}
                                                    className="w-full bg-slate-50 border border-slate-200 rounded-lg py-2 px-3 text-xs font-bold text-slate-700 outline-none"
                                                >
                                                    <option value="">Select Target Table...</option>
                                                    {fkTargetTables.filter(t => t.name !== tableName).map(t => (
                                                        <option key={t.name} value={t.name}>{t.name}</option>
                                                    ))}
                                                </select>
                                            </div>

                                            {/* Column Selector */}
                                            {col.foreignKey?.table && (
                                                <div className="space-y-1">
                                                    <label className="text-[9px] font-black text-blue-400 uppercase tracking-widest ml-0.5">Column</label>
                                                    {fkLoading
                                                        ? <div className="py-2 flex justify-center"><Loader2 size={12} className="animate-spin text-indigo-500" /></div>
                                                        : (
                                                            <select
                                                                value={col.foreignKey.column}
                                                                onChange={(e: any) => {
                                                                    const fkSchema = col.foreignKey?.schema || activeSchema;
                                                                    handleSetForeignKey(col.id, fkSchema, col.foreignKey!.table, e.target.value);
                                                                }}
                                                                className="w-full bg-slate-50 border-none rounded-lg py-2 px-3 text-xs font-mono font-bold outline-none"
                                                            >
                                                                <option value="">Select Column...</option>
                                                                {fkTargetColumns.map(c => <option key={c} value={c}>{c}</option>)}
                                                            </select>
                                                        )
                                                    }
                                                </div>
                                            )}

                                            <div className="flex justify-between items-center pt-2 border-t border-slate-100">
                                                <button onClick={() => { handleSetForeignKey(col.id, '', '', ''); setActiveFkEditor(null); }} className="text-[10px] font-bold text-rose-500 hover:underline">Remove Link</button>
                                                <button onClick={() => setActiveFkEditor(null)} className="px-3 py-1.5 bg-indigo-600 text-white text-[10px] font-black rounded-lg hover:bg-indigo-700 transition-colors">OK</button>
                                            </div>
                                        </div>
                                    </div>
                                )}
                            </div>
                        );})}
                    </div>

                    {/* Add Column Button */}
                    <button
                        onClick={handleAddColumn}
                        className="w-full py-3 border border-dashed border-slate-300 rounded-xl text-slate-400 text-xs font-bold hover:bg-slate-50 hover:text-indigo-600 hover:border-indigo-300 transition-all flex items-center justify-center gap-2"
                    >
                        <Plus size={14} /> Add Column
                    </button>
                </div>

                {/* MCP Access Card */}
                {mcpEnabled && (
                    <div className="bg-slate-900 p-4 rounded-xl border border-slate-700">
                        <div className="flex items-center gap-3 mb-3">
                            <div className="w-8 h-8 bg-emerald-500 rounded-lg flex items-center justify-center shadow-lg shadow-emerald-500/30">
                                <Cpu size={16} className="text-white" />
                            </div>
                            <div>
                                <span className="text-xs font-bold text-white block">MCP Access (AI Agents)</span>
                                <span className="text-[10px] text-slate-400 font-medium">Permissions for Cursor, Windsurf, etc.</span>
                            </div>
                        </div>
                        <div className="flex gap-2">
                            {(['r', 'c', 'u', 'd'] as const).map(perm => {
                                const labels = { r: 'READ', c: 'CREATE', u: 'UPDATE', d: 'DELETE' };
                                const colors = {
                                    r: mcpPerms[perm] ? 'bg-blue-500 text-white border-blue-400' : 'bg-slate-800 text-slate-500 border-slate-600',
                                    c: mcpPerms[perm] ? 'bg-emerald-500 text-white border-emerald-400' : 'bg-slate-800 text-slate-500 border-slate-600',
                                    u: mcpPerms[perm] ? 'bg-amber-500 text-white border-amber-400' : 'bg-slate-800 text-slate-500 border-slate-600',
                                    d: mcpPerms[perm] ? 'bg-rose-500 text-white border-rose-400' : 'bg-slate-800 text-slate-500 border-slate-600',
                                };
                                return (
                                    <button
                                        key={perm}
                                        onClick={() => setMcpPerms((prev: any) => ({ ...prev, [perm]: !prev[perm] }))}
                                        className={`flex-1 py-2 rounded-lg text-[10px] font-black uppercase tracking-widest border transition-all ${colors[perm]}`}
                                    >
                                        {labels[perm]}
                                    </button>
                                );
                            })}
                        </div>
                        {mcpPerms.d && (
                            <p className="text-[9px] text-rose-400 font-bold mt-2 text-center animate-pulse">
                                ⚠ DELETE enabled — AI agents will be able to delete rows
                            </p>
                        )}
                    </div>
                )}

                {/* Table Step-Up Security */}
                <div className="bg-white p-4 rounded-xl border border-rose-200">
                    <div className="flex items-center justify-between gap-4">
                        <div className="flex items-center gap-3">
                            {tableSecurityEnabled
                                ? <ShieldCheck size={18} className="text-rose-600" />
                                : <ShieldOff size={18} className="text-slate-400" />
                            }
                            <div>
                                <span className="text-xs font-bold text-slate-700 block">Table Step-Up Security</span>
                                <span className="text-[10px] text-slate-400 font-medium">
                                    {tableSecurityEnabled ? 'Requires OTP, passkey, or TOTP before selected CRUD operations' : 'No table-level step-up required'}
                                </span>
                            </div>
                        </div>
                        <button
                            onClick={() => setTableSecurityEnabled(!tableSecurityEnabled)}
                            className={`w-12 h-7 rounded-full p-1 transition-colors ${tableSecurityEnabled ? 'bg-rose-600' : 'bg-slate-200'}`}
                        >
                            <div className={`w-5 h-5 bg-white rounded-full shadow-sm transition-transform ${tableSecurityEnabled ? 'translate-x-5' : ''}`}></div>
                        </button>
                    </div>
                    {tableSecurityEnabled && (
                        <div className="mt-4 grid grid-cols-1 gap-4">
                            <div>
                                <label className="text-[9px] font-black text-rose-500 uppercase tracking-widest block mb-2">
                                    Protected operations
                                </label>
                                <div className="grid grid-cols-4 gap-2">
                                    {[
                                        { value: 'read', label: 'READ' },
                                        { value: 'create', label: 'CREATE' },
                                        { value: 'update', label: 'UPDATE' },
                                        { value: 'delete', label: 'DELETE' },
                                    ].map(op => {
                                        const active = tableSecurityOperations.includes(op.value);
                                        return (
                                            <button
                                                key={op.value}
                                                type="button"
                                                onClick={() => {
                                                    setTableSecurityOperations(prev => active
                                                        ? prev.filter(v => v !== op.value)
                                                        : [...prev, op.value]
                                                    );
                                                }}
                                                className={`py-2 rounded-lg text-[10px] font-black border transition-colors ${active ? 'bg-rose-600 text-white border-rose-600' : 'bg-slate-50 text-slate-500 border-slate-200'}`}
                                            >
                                                {op.label}
                                            </button>
                                        );
                                    })}
                                </div>
                            </div>
                            <div>
                                <label className="text-[9px] font-black text-rose-500 uppercase tracking-widest block mb-2">
                                    Accepted factors
                                </label>
                                <div className="flex flex-wrap gap-2">
                                    {ChallengeFactorOptions.map(factor => {
                                        const active = tableSecurityFactors.includes(factor.value);
                                        return (
                                            <label key={factor.value} className="flex items-center gap-1 cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    checked={active}
                                                    onChange={(e) => {
                                                        setTableSecurityFactors(prev => e.target.checked
                                                            ? [...prev, factor.value]
                                                            : prev.filter(v => v !== factor.value)
                                                        );
                                                    }}
                                                    className="text-rose-500 rounded-sm outline-none"
                                                />
                                                <span className="text-[10px] font-bold text-slate-600">{factor.label}</span>
                                            </label>
                                        );
                                    })}
                                </div>
                            </div>
                        </div>
                    )}
                </div>

                {/* RLS Toggle */}
                <div className="flex items-center justify-between bg-slate-50 p-4 rounded-xl border border-slate-200">
                    <div className="flex items-center gap-3">
                        {enableRLS
                            ? <Shield size={18} className="text-emerald-600" />
                            : <ShieldOff size={18} className="text-slate-400" />
                        }
                        <div>
                            <span className="text-xs font-bold text-slate-700 block">Row Level Security</span>
                            <span className="text-[10px] text-slate-400 font-medium">{enableRLS ? 'Enabled — recommended for multi-tenant' : 'Disabled — open access'}</span>
                        </div>
                    </div>
                    <button
                        onClick={() => setEnableRLS(!enableRLS)}
                        className={`w-12 h-7 rounded-full p-1 transition-colors ${enableRLS ? 'bg-emerald-600' : 'bg-slate-200'}`}
                    >
                        <div className={`w-5 h-5 bg-white rounded-full shadow-sm transition-transform ${enableRLS ? 'translate-x-5' : ''}`}></div>
                    </button>
                </div>

                {/* RLS Suggestions Toggle */}
                {enableRLS && (
                    <div className="flex items-center justify-between bg-slate-50 p-4 rounded-xl border border-slate-200">
                        <div className="flex items-center gap-3">
                            {enableRlsSuggestions
                                ? <Sparkles size={18} className="text-indigo-600" />
                                : <Sparkles size={18} className="text-slate-400" />
                            }
                            <div>
                                <span className="text-xs font-bold text-slate-700 block">Auto-Generate Policies</span>
                                <span className="text-[10px] text-slate-400 font-medium">{enableRlsSuggestions ? 'Enabled — suggestions based on table structure' : 'Disabled — write policies manually'}</span>
                            </div>
                        </div>
                        <button
                            onClick={() => {
                                const nextValue = !enableRlsSuggestions;
                                setEnableRlsSuggestions(nextValue);
                                if (nextValue) {
                                    const hasAutoUser = columns.some(c => c.autoUser);
                                    if (!hasAutoUser) {
                                        const newCol: ColumnDef = {
                                            id: getUUID(),
                                            name: 'user_id',
                                            type: 'uuid',
                                            isPrimaryKey: false,
                                            isNullable: false,
                                            isUnique: false,
                                            defaultValue: '',
                                            isArray: false,
                                            autoUser: true
                                        };
                                        setColumns(prev => [...prev, newCol]);
                                    }
                                }
                            }}
                            className={`w-12 h-7 rounded-full p-1 transition-colors ${enableRlsSuggestions ? 'bg-indigo-600' : 'bg-slate-200'}`}
                        >
                            <div className={`w-5 h-5 bg-white rounded-full shadow-sm transition-transform ${enableRlsSuggestions ? 'translate-x-5' : ''}`}></div>
                        </button>
                    </div>
                )}

                {/* RLS Policy Suggestions */}
                {enableRLS && enableRlsSuggestions && existingRlsPolicies.length === 0 && (
                    <div className="bg-indigo-50 border border-indigo-200 rounded-xl p-4">
                        <div className="flex items-center gap-3 mb-3">
                            <ShieldCheck size={18} className="text-indigo-600" />
                            <div>
                                <span className="text-xs font-bold text-indigo-700 block">Security Policies</span>
                                <span className="text-[10px] text-indigo-400 font-medium">Auto-generated based on table structure</span>
                            </div>
                        </div>

                        {/* Owner Column Selection */}
                        {(getAuthUserColumns().length > 0 || columns.some(c => c.autoUser) || Object.values(rlsPolicies).some(v => v === 'owner_only')) && (
                            <div className="mb-4 p-3 bg-white rounded-lg border border-indigo-100">
                                <div className="text-[10px] font-bold text-indigo-700 uppercase tracking-widest mb-2">Proprietário (Owner Column)</div>
                                <div className="space-y-2">
                                    {columns.some(c => c.autoUser) && (
                                        <label className="flex items-center gap-2 text-xs">
                                            <input
                                                type="radio"
                                                name="ownerType"
                                                checked={selectedOwnerColumns.length === 0}
                                                onChange={() => setSelectedOwnerColumns([])}
                                                className="text-indigo-600"
                                            />
                                            <span className="font-medium">👤 Auto User (auth.uid())</span>
                                            <span className="text-[9px] text-slate-400">- Coluna com DEFAULT auth.uid()</span>
                                        </label>
                                    )}
                                    
                                    {getAuthUserColumns().map(col => (
                                        <label key={col.id} className="flex items-center gap-2 text-xs">
                                            <input
                                                type="checkbox"
                                                checked={selectedOwnerColumns.includes(col.id)}
                                                onChange={(e) => {
                                                    if (e.target.checked) {
                                                        setSelectedOwnerColumns([...selectedOwnerColumns, col.id]);
                                                    } else {
                                                        setSelectedOwnerColumns(selectedOwnerColumns.filter(id => id !== col.id));
                                                    }
                                                }}
                                                className="text-indigo-600"
                                                disabled={selectedOwnerColumns.length === 0 && !columns.some(c => c.autoUser)}
                                            />
                                            <span className="font-medium">🔗 {col.name}</span>
                                            <span className="text-[9px] text-slate-400">- FK para auth.users({col.foreignKey?.column})</span>
                                        </label>
                                    ))}
                                </div>
                                <div className="text-[9px] text-indigo-600 mt-2">
                                    💡 Selecione qual(is) coluna(s) definem o "dono" da linha
                                </div>
                            </div>
                        )}
                        
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {/* SELECT Policies */}
                            <div className="space-y-2">
                                <div className="text-[10px] font-bold text-slate-700 uppercase tracking-widest mb-2">SELECT (Read Access)</div>
                                <div className="p-2 bg-white rounded-lg border border-slate-200">
                                    <select
                                        value={rlsPolicies.select}
                                        onChange={(e) => setRlsPolicies(prev => ({ ...prev, select: e.target.value }))}
                                        className="w-full bg-transparent text-xs font-medium text-slate-700 outline-none"
                                    >
                                        <option value="none">🔒 No access (Deny All)</option>
                                        <option value="owner_only">👤 Only owner can read their own rows</option>
                                        <option value="authenticated">🔑 Any authenticated user can read</option>
                                        <option value="public">🌐 Public access (Anyone can read)</option>
                                    </select>
                                    <div className="text-[9px] text-slate-400 mt-1 font-mono">
                                        {getPolicyClausePreview('SELECT', rlsPolicies.select)}
                                    </div>
                                </div>
                            </div>

                            {/* INSERT Policies */}
                            <div className="space-y-2">
                                <div className="text-[10px] font-bold text-slate-700 uppercase tracking-widest mb-2">INSERT (Create Access)</div>
                                <div className="p-2 bg-white rounded-lg border border-slate-200">
                                    <select
                                        value={rlsPolicies.insert}
                                        onChange={(e) => setRlsPolicies(prev => ({ ...prev, insert: e.target.value }))}
                                        className="w-full bg-transparent text-xs font-medium text-slate-700 outline-none"
                                    >
                                        <option value="none">🔒 No access (Deny All)</option>
                                        <option value="authenticated">🔑 Any authenticated can create</option>
                                        <option value="blocked">🚫 Blocked via API (Only service_role)</option>
                                    </select>
                                    <div className="text-[9px] text-slate-400 mt-1 font-mono">
                                        {getPolicyClausePreview('INSERT', rlsPolicies.insert)}
                                    </div>
                                </div>
                            </div>

                            {/* UPDATE Policies */}
                            <div className="space-y-2">
                                <div className="text-[10px] font-bold text-slate-700 uppercase tracking-widest mb-2">UPDATE (Modify Access)</div>
                                <div className="p-2 bg-white rounded-lg border border-slate-200">
                                    <select
                                        value={rlsPolicies.update}
                                        onChange={(e) => setRlsPolicies(prev => ({ ...prev, update: e.target.value }))}
                                        className="w-full bg-transparent text-xs font-medium text-slate-700 outline-none"
                                    >
                                        <option value="none">🔒 No access (Deny All)</option>
                                        <option value="owner_only">👤 Only owner can modify their own rows</option>
                                        <option value="authenticated">🔑 Any authenticated can modify</option>
                                        <option value="blocked">🚫 Blocked via API (Only service_role)</option>
                                    </select>
                                    <div className="text-[9px] text-slate-400 mt-1 font-mono">
                                        {getPolicyClausePreview('UPDATE', rlsPolicies.update)}
                                    </div>
                                </div>
                            </div>

                            {/* DELETE Policies */}
                            <div className="space-y-2">
                                <div className="text-[10px] font-bold text-slate-700 uppercase tracking-widest mb-2">DELETE (Remove Access)</div>
                                <div className="p-2 bg-white rounded-lg border border-slate-200">
                                    <select
                                        value={rlsPolicies.delete}
                                        onChange={(e) => setRlsPolicies(prev => ({ ...prev, delete: e.target.value }))}
                                        className="w-full bg-transparent text-xs font-medium text-slate-700 outline-none"
                                    >
                                        <option value="none">🔒 No access (Deny All)</option>
                                        <option value="owner_only">👤 Only owner can delete their own rows</option>
                                        <option value="blocked">🚫 Blocked via API (Only service_role)</option>
                                    </select>
                                    <div className="text-[9px] text-slate-400 mt-1 font-mono">
                                        {getPolicyClausePreview('DELETE', rlsPolicies.delete)}
                                    </div>
                                </div>
                            </div>
                        </div>
                        
                        <div className="mt-4 p-3 bg-amber-50 border border-amber-200 rounded-lg">
                            <p className="text-[10px] text-amber-700 font-medium">
                                💡 Policies are automatically generated when you create the table. You can customize the SQL after generation if needed.
                            </p>
                        </div>
                    </div>
                )}

                {/* RLS Policies Preview (when existing policies are present) */}
                {enableRLS && existingRlsPolicies.length > 0 && (
                    <div className="bg-indigo-50 border border-indigo-200 rounded-xl p-4">
                        <div className="flex items-center gap-3 mb-3">
                            <ShieldCheck size={18} className="text-indigo-600" />
                            <div>
                                <span className="text-xs font-bold text-indigo-700 block">Security Policies (Existing)</span>
                                <span className="text-[10px] text-indigo-400 font-medium">Policies detected from SQL Console</span>
                            </div>
                        </div>

                        <div className="bg-slate-900 rounded-lg p-4 overflow-auto max-h-64">
                            <pre className="text-[10px] font-mono text-emerald-400 whitespace-pre-wrap">
                                {existingRlsPolicies.join('\n\n')}
                            </pre>
                        </div>

                        <div className="mt-3 p-3 bg-amber-50 border border-amber-200 rounded-lg">
                            <p className="text-[10px] text-amber-700 font-medium">
                                💡 These policies will be included in the generated SQL. They are read-only in this view.
                            </p>
                        </div>
                    </div>
                )}
            </div>

            {/* Footer */}
            <div className="p-6 border-t border-slate-100 bg-slate-50/50 space-y-3">
                <div className="flex gap-4">
                    <button onClick={onClose} className="flex-1 py-3 text-xs font-black text-slate-400 uppercase tracking-widest hover:text-slate-600">Cancel</button>
                    <button
                        onClick={generateSQL}
                        disabled={!canGenerate}
                        className="flex-[2] bg-indigo-600 text-white py-3 rounded-xl text-xs font-black uppercase tracking-widest shadow-xl hover:bg-indigo-700 transition-all flex items-center justify-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed"
                    >
                        Generate & Execute SQL
                    </button>
                </div>
                <div className="flex items-center justify-center gap-4 text-[9px] font-bold text-slate-400">
                    <span className="flex items-center gap-1"><kbd className="px-1.5 py-0.5 bg-slate-200 rounded text-[8px] font-black text-slate-500">Ctrl+Enter</kbd> Execute</span>
                    <span className="text-slate-200">·</span>
                    <span className="flex items-center gap-1"><kbd className="px-1.5 py-0.5 bg-slate-200 rounded text-[8px] font-black text-slate-500">Ctrl+S</kbd> Save to Editor</span>
                </div>
            </div>

            {/* Validation hint */}
            {hasEmptyColumn && tableName && (
                <div className="px-6 pb-4 -mt-2">
                    <p className="text-[10px] font-bold text-amber-600 text-center">⚠ All columns must have a name</p>
                </div>
            )}
        </div>
    );
};

export default TableCreatorDrawer;
