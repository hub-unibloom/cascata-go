
import React, { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { EdgeHistoryModal } from '../components/EdgeHistoryModal';
import {
    Code2, Play, Plus, Clock, Terminal, Loader2, Folder, FolderPlus,
    ChevronRight, ChevronDown, FileCode, Search, Trash2, Copy,
    CheckCircle2, X, Zap, Save, Cpu, Globe, Ghost, ClipboardPaste,
    Lock, Eraser, Link as LinkIcon, Edit, AlertCircle, Key,
    History, Sparkles, Box, Layout, FileText, ChevronUp, GripHorizontal,
    Split, MonitorPlay, Move, CornerDownRight, ToggleLeft, ToggleRight, Calendar,
    AlertTriangle, GitCompare, Eye, EyeOff
} from 'lucide-react';

type AssetType = 'rpc' | 'trigger' | 'cron' | 'folder' | 'edge_function' | 'snippet';

interface ProjectAsset {
    id: string;
    name: string;
    type: AssetType;
    parent_id: string | null;
    isUnmanaged?: boolean;
    isFromDatabase?: boolean;
    metadata: {
        notes?: string;
        sql?: string;
        db_object_name?: string;
        env_vars?: Record<string, string>;
        timeout_ms?: number;
        schedule?: string;
        imports?: string[];
        source?: 'database' | 'manual';
        table_name?: string;
        runtime?: string;
        status?: string;
    };
}

interface AssetTreeNode extends ProjectAsset {
    children: AssetTreeNode[];
}

const IGNORED_PREFIXES = ['uuid_', 'pg_', 'armor', 'crypt', 'digest', 'hmac', 'gen_', 'encrypt', 'decrypt', 'notify_', 'pissh_', 'dearmor', 'fips_mode'];

// --- ERROR TRANSLATOR UTILITY ---
const humanizePostgresError = (err: any) => {
    const msg = err.message || JSON.stringify(err);
    if (msg.includes('42P01')) return "Erro: A tabela ou objeto referenciado não existe no banco.";
    if (msg.includes('42703')) return "Erro: Uma coluna citada no código não existe.";
    if (msg.includes('23505')) return "Conflito: Já existe um registro com este identificador único.";
    if (msg.includes('22P02')) return "Erro de Tipo: Valor inválido para o tipo de dado esperado (ex: texto em campo numérico ou UUID inválido).";
    if (msg.includes('42601')) return "Erro de Sintaxe: O código SQL contém erros gramaticais.";
    if (msg.includes('not unique')) return "Conflito de Nome: Já existe uma função com este nome mas parâmetros diferentes. Use o botão Salvar para resolver o conflito.";
    return msg; // Fallback
};

const RPCManager: React.FC<{ projectId: string, currentEnv?: string }> = ({ projectId, currentEnv }) => {
    // --- GLOBAL STATE ---
    const [activeContext, setActiveContext] = useState<AssetType | 'all'>('all');
    const [assets, setAssets] = useState<ProjectAsset[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [successMsg, setSuccessMsg] = useState<string | null>(null);
    const [projectKeys, setProjectKeys] = useState<{ anon: string, service: string } | null>(null);

    // --- EDITOR STATE ---
    const [selectedAsset, setSelectedAsset] = useState<ProjectAsset | null>(null);
    const [editorSql, setEditorSql] = useState('');
    const [originalSql, setOriginalSql] = useState('');
    const [isDirty, setIsDirty] = useState(false);

    // --- UI LAYOUT STATE ---
    // Load persistent height or default to 350
    const [bottomPanelHeight, setBottomPanelHeight] = useState(() => {
        const saved = localStorage.getItem('cascata_rpc_panel_height');
        return saved ? parseInt(saved) : 350;
    });
    const [isResizingPanel, setIsResizingPanel] = useState(false);
    const [activeBottomTab, setActiveBottomTab] = useState<'result' | 'logs' | 'params' | 'form'>('form');
    const [showHistoryDropdown, setShowHistoryDropdown] = useState(false);
    const [showHistoryModal, setShowHistoryModal] = useState(false);

    // --- EXECUTION STATE ---
    const [executing, setExecuting] = useState(false);
    const [testParams, setTestParams] = useState('{}');
    const [testResult, setTestResult] = useState<any>(null);
    const [executionLogs, setExecutionLogs] = useState<string[]>([]);

    // --- CONFIG STATE ---
    const [notes, setNotes] = useState('');
    const [envVars, setEnvVars] = useState<Record<string, string>>({});
    const [edgeImports, setEdgeImports] = useState<string[]>([]);
    const [newImport, setNewImport] = useState('');
    const [timeoutSec, setTimeoutSec] = useState(5);
    const [cronSchedule, setCronSchedule] = useState('* * * * *');

    // --- TREE & DRAG STATE ---
    const [expandedFolders, setExpandedFolders] = useState<Set<string>>(new Set());
    const [draggedItem, setDraggedItem] = useState<string | null>(null);
    const [dragOverItem, setDragOverItem] = useState<string | null>(null);
    const [contextMenu, setContextMenu] = useState<{ x: number, y: number, item: AssetTreeNode } | null>(null);

    // --- MODALS ---
    const [showRenameModal, setShowRenameModal] = useState(false);
    const [renameValue, setRenameValue] = useState('');
    const [showDeleteModal, setShowDeleteModal] = useState(false);
    const [showMoveModal, setShowMoveModal] = useState(false);
    const [targetAsset, setTargetAsset] = useState<ProjectAsset | null>(null);
    const [moveTargetFolder, setMoveTargetFolder] = useState<string>('');

    // --- CONFLICT MODAL STATE ---
    const [showConflictModal, setShowConflictModal] = useState(false);
    const [conflictData, setConflictData] = useState<{ oldCode: string, newCode: string, oldArgs: string, newArgs: string } | null>(null);

    // --- SYNC FROM DATABASE STATE ---
    const [showSyncModal, setShowSyncModal] = useState(false);
    const [syncItems, setSyncItems] = useState<{name: string, type: 'rpc' | 'trigger', checked: boolean, existing: boolean}[]>([]);
    const [syncLoading, setSyncLoading] = useState(false);

    // --- GLOBAL SECRETS MODAL ---
    const [showSecretsModal, setShowSecretsModal] = useState(false);
    const [globalSecrets, setGlobalSecrets] = useState<any[]>([]);
    const [newSecretKey, setNewSecretKey] = useState('');
    const [newSecretVal, setNewSecretVal] = useState('');
    const [revealedSecrets, setRevealedSecrets] = useState<Record<string, string>>({});

    // --- REFS ---
    const editorRef = useRef<HTMLTextAreaElement>(null);
    const lineNumbersRef = useRef<HTMLDivElement>(null);

    // --- API HELPER ---
    const fetchWithAuth = useCallback(async (url: string, options: any = {}) => {
        const token = localStorage.getItem('cascata_token');
        const response = await fetch(url, {
            ...options,
            headers: { ...options.headers, 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' }
        });
        if (!response.ok) {
            const errData = await response.json().catch(() => ({}));
            throw new Error(errData.error || `Error ${response.status}`);
        }
        return response.json();
    }, []);

    // --- INITIAL LOAD ---
    const fetchData = async () => {
        setLoading(true);
        try {
            const [assetsData, functionsData, triggersData, cronJobsData, edgeFunctionsData, projectData, vaultData] = await Promise.all([
                fetchWithAuth(`/api/data/${projectId}/assets`),
                fetchWithAuth(`/api/data/${projectId}/functions`),
                fetchWithAuth(`/api/data/${projectId}/triggers`),
                fetchWithAuth(`/api/data/${projectId}/cron-jobs`),
                fetchWithAuth(`/api/data/${projectId}/edge-functions`),
                fetchWithAuth('/api/control/projects'),
                fetchWithAuth(`/api/control/projects/${projectId}/vault`)
            ]);

            // Extract Keys & Global Secrets
            const currentProj = projectData.find((p: any) => p.slug === projectId);
            if (currentProj) {
                setProjectKeys({ anon: currentProj.anon_key, service: currentProj.service_key });
            }
            setGlobalSecrets(vaultData.filter((s: any) => s.type !== 'folder'));

            const combinedAssets: ProjectAsset[] = [...assetsData];
            const managedNames = new Set(assetsData.map((a: any) => a.name));

            // Add native functions with DDL from backend
            functionsData.forEach((fn: any) => {
                if (!managedNames.has(fn.name) && !IGNORED_PREFIXES.some(p => fn.name.startsWith(p))) {
                    combinedAssets.push({ 
                        id: `native_rpc_${fn.name}`, 
                        name: fn.name, 
                        type: 'rpc', 
                        parent_id: fn.parent_id || null, 
                        isUnmanaged: true, 
                        metadata: { sql: fn.ddl, source: 'database' } 
                    });
                }
            });

            // Add native triggers with DDL from backend
            triggersData.forEach((tr: any) => {
                if (!managedNames.has(tr.name)) {
                    combinedAssets.push({ 
                        id: `native_trig_${tr.name}`, 
                        name: tr.name, 
                        type: 'trigger', 
                        parent_id: tr.parent_id || null, 
                        isUnmanaged: true, 
                        metadata: { sql: tr.ddl, source: 'database', table_name: tr.table_name } 
                    });
                }
            });

            // Add native cron jobs from pg_cron (cascata_system)
            cronJobsData.forEach((cj: any) => {
                const cleanJobname = cj.jobname.replace(new RegExp(`-${projectId}$`), ''); // Remove tenant suffix for display
                if (!managedNames.has(cj.jobname)) {
                    combinedAssets.push({ 
                        id: `native_cron_${cj.jobid}`, 
                        name: cleanJobname, 
                        type: 'cron', 
                        parent_id: cj.parent_id || null, 
                        isUnmanaged: true,
                        isFromDatabase: true,
                        metadata: { 
                            sql: `SELECT cron.schedule('${cj.jobname}', '${cj.schedule}', $$${cj.command}$$)`, 
                            source: 'database',
                            schedule: cj.schedule,
                            db_object_name: cj.jobname
                        } 
                    });
                }
            });

            // Add edge functions from system.edge_functions
            edgeFunctionsData.forEach((ef: any) => {
                combinedAssets.push({
                        id: `edge_func_${ef.id}`,
                        name: ef.name,
                        type: 'edge_function',
                        parent_id: null,
                        isUnmanaged: true,
                        isFromDatabase: true,
                        metadata: {
                            sql: '// Edge function code - click to load from server',
                            source: 'database',
                            runtime: ef.runtime,
                            status: ef.status,
                            timeout_ms: ef.timeout_ms,
                            env_vars: ef.env_vars
                        }
                    });
            });

            setAssets(combinedAssets);
        } catch (err: any) { setError(humanizePostgresError(err)); }
        finally { setLoading(false); }
    };

    useEffect(() => {
        fetchData();
        const hide = () => { setContextMenu(null); setShowHistoryDropdown(false); };
        window.addEventListener('click', hide);
        return () => window.removeEventListener('click', hide);
    }, [projectId]);

    // --- HOTKEYS ---
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                e.preventDefault();
                if (isDirty && selectedAsset) handleSaveObject();
            }
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                e.preventDefault();
                if (selectedAsset) executeTest();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [isDirty, selectedAsset, editorSql, testParams]);

    // --- RESIZE LOGIC (PERSISTENT & FLEXIBLE) ---
    useEffect(() => {
        const handleMove = (e: MouseEvent) => {
            if (!isResizingPanel) return;
            // Flexible limits: Min 50px, Max 90% of screen
            const maxH = window.innerHeight * 0.9;
            const newHeight = Math.max(50, Math.min(window.innerHeight - e.clientY, maxH));
            setBottomPanelHeight(newHeight);
        };
        const handleUp = () => {
            setIsResizingPanel(false);
            // Persist on release
            localStorage.setItem('cascata_rpc_panel_height', bottomPanelHeight.toString());
        };
        if (isResizingPanel) {
            document.addEventListener('mousemove', handleMove);
            document.addEventListener('mouseup', handleUp);
            document.body.style.cursor = 'row-resize';
        } else {
            document.body.style.cursor = 'default';
        }
        return () => {
            document.removeEventListener('mousemove', handleMove);
            document.removeEventListener('mouseup', handleUp);
        };
    }, [isResizingPanel, bottomPanelHeight]);

    // --- SYNC SCROLL (IDE FEEL) ---
    const handleEditorScroll = () => {
        if (editorRef.current && lineNumbersRef.current) {
            lineNumbersRef.current.scrollTop = editorRef.current.scrollTop;
        }
    };

    // --- DIRTY CHECK ---
    useEffect(() => {
        setIsDirty(editorSql.trim() !== originalSql.trim());
    }, [editorSql, originalSql]);

    // --- LOAD ASSET & PARAMS PERSISTENCE ---
    const loadDefinition = async (asset: ProjectAsset) => {
        setExecuting(true);
        try {
            let code = '';
            if (asset.isUnmanaged) {
                // Cron jobs nativos já têm SQL completo no metadata
                if (asset.type === 'cron') {
                    code = asset.metadata?.sql || '-- Cron job SQL not available';
                } else if (asset.type === 'edge_function') {
                    // Edge functions agora têm endpoint para carregar código do servidor
                    const res = await fetchWithAuth(`/api/data/${projectId}/edge-functions/${asset.name}`);
                    code = res.content || asset.metadata?.sql || '// Edge function code not available';
                    
                    // Sincroniza metadados atualizados vindos do servidor diretamente para os states do editor
                    setEnvVars(res.env_vars || {});
                    setEdgeImports(res.imports || []);
                    setTimeoutSec((res.timeout_ms || 5000) / 1000);
                    
                    // Sincroniza metadados no próprio asset selecionado
                    asset.metadata = {
                        ...asset.metadata,
                        env_vars: res.env_vars || {},
                        timeout_ms: res.timeout_ms || 5000,
                        imports: res.imports || [],
                        status: res.status || 'active'
                    };
                } else {
                    const endpoint = asset.type === 'rpc' ? 'rpc' : 'trigger';
                    const res = await fetchWithAuth(`/api/data/${projectId}/${endpoint}/${asset.name}/definition`);
                    code = res.definition || '-- Source code not found';
                }
            } else {
                code = asset.metadata?.sql || '';
            }
            setEditorSql(code);
            setOriginalSql(code);
            setEdgeImports(asset.metadata?.imports || []);

            // Load Saved Params for this specific asset
            const savedParams = localStorage.getItem(`cascata_params_${asset.id}`);
            if (savedParams) {
                setTestParams(savedParams);
            } else {
                // Generate default structure if no saved params
                setTestParams('{}');
            }

        } catch (e) {
            setEditorSql('-- Failed to load source code.');
        } finally {
            setExecuting(false);
        }
    };

    // Save Params on Change
    useEffect(() => {
        if (selectedAsset?.id) {
            localStorage.setItem(`cascata_params_${selectedAsset.id}`, testParams);
        }
    }, [testParams, selectedAsset]);

    // --- ACTIONS ---

    // Helper to extract function signature for comparison
    const extractSignature = (sql: string) => {
        const match = sql.match(/FUNCTION\s+(?:public\.)?[\w_]+\s*(\(.*?\))/is);
        return match ? match[1].replace(/\s+/g, ' ').trim() : '()';
    };

    const handleToggleStatus = async () => {
        if (!selectedAsset) return;
        const currentStatus = selectedAsset.metadata?.status;
        const nextStatus = currentStatus === 'inactive' ? 'active' : 'inactive';
        setExecuting(true);
        try {
            let updatedAsset;
            if (selectedAsset.type === 'edge_function') {
                await fetchWithAuth(`/api/data/${projectId}/edge-functions/deploy`, {
                    method: 'POST',
                    body: JSON.stringify({
                        name: selectedAsset.name,
                        content: editorSql,
                        env_vars: envVars,
                        imports: edgeImports,
                        timeout_ms: timeoutSec * 1000,
                        status: nextStatus
                    })
                });
                updatedAsset = {
                    ...selectedAsset,
                    metadata: {
                        ...selectedAsset.metadata,
                        status: nextStatus
                    }
                };
            } else {
                updatedAsset = await fetchWithAuth(`/api/data/${projectId}/assets`, {
                    method: 'POST',
                    body: JSON.stringify({
                        ...selectedAsset,
                        id: selectedAsset.isUnmanaged ? undefined : selectedAsset.id,
                        metadata: {
                            ...selectedAsset.metadata,
                            status: nextStatus
                        }
                    })
                });
            }
            
            setAssets(prev => prev.map(a => (a.id === selectedAsset.id ? updatedAsset : a)));
            setSelectedAsset(updatedAsset);
            setSuccessMsg(`Status updated to "${nextStatus}"`);
            setTimeout(() => setSuccessMsg(null), 2000);
            fetchData();
        } catch (err: any) {
            setError(humanizePostgresError(err));
        } finally {
            setExecuting(false);
        }
    };

    const handleSaveObject = async (forceOverwrite: boolean = false) => {
        if (!selectedAsset) return;
        setExecuting(true);

        try {
            const detectedName = selectedAsset.type === 'edge_function' ? selectedAsset.name : (extractObjectName(editorSql) || selectedAsset.name);

            // CONFLICT CHECK (Only for RPCs)
            if (!forceOverwrite && selectedAsset.type === 'rpc') {
                // Check if function already exists in system assets or native definition
                const existing = assets.find(a => a.name === detectedName && a.id !== selectedAsset.id);
                const isSameName = detectedName === selectedAsset.name; // Editing same file

                if (existing || isSameName) {
                    // Fetch current definition from DB to compare
                    try {
                        const res = await fetchWithAuth(`/api/data/${projectId}/rpc/${detectedName}/definition`);
                        if (res.definition) {
                            const oldArgs = extractSignature(res.definition);
                            const newArgs = extractSignature(editorSql);

                            // If args are different, we have a conflict (Overloading)
                            if (oldArgs !== newArgs) {
                                setConflictData({
                                    oldCode: res.definition,
                                    newCode: editorSql,
                                    oldArgs,
                                    newArgs
                                });
                                setShowConflictModal(true);
                                setExecuting(false);
                                return; // STOP SAVE
                            }
                        }
                    } catch (e) { /* Ignore if not found, safe to save */ }
                }
            }

            // If we are here, either no conflict or forceOverwrite is true
            if (forceOverwrite && selectedAsset.type === 'rpc') {
                // Explicitly Drop Old Function first to prevent overloading
                await fetchWithAuth(`/api/data/${projectId}/query`, {
                    method: 'POST',
                    body: JSON.stringify({ sql: `DROP FUNCTION IF EXISTS public.${detectedName} CASCADE;` })
                });
            }

            // Execute SQL Creation
            if (['rpc', 'trigger', 'cron'].includes(selectedAsset.type)) {
                let sqlToExecute = editorSql;
                
                // For triggers: PostgreSQL doesn't support CREATE OR REPLACE TRIGGER
                // Need to drop first if it already exists
                if (selectedAsset.type === 'trigger') {
                    // Extract trigger name and table from the CREATE TRIGGER statement
                    const triggerMatch = editorSql.match(/CREATE\s+(?:OR\s+REPLACE\s+)?TRIGGER\s+(\w+)/i);
                    const triggerName = triggerMatch ? triggerMatch[1] : selectedAsset.name;
                    
                    // Get table name from metadata or extract from SQL
                    let tableName = selectedAsset.metadata?.table_name;
                    if (!tableName) {
                        const tableMatch = editorSql.match(/ON\s+(\w+)/i);
                        tableName = tableMatch ? tableMatch[1] : null;
                    }
                    
                    if (tableName) {
                        // Prepend DROP TRIGGER IF EXISTS
                        sqlToExecute = `DROP TRIGGER IF EXISTS ${triggerName} ON ${tableName};\n${editorSql}`;
                    }
                }
                
                await fetchWithAuth(`/api/data/${projectId}/query`, {
                    method: 'POST',
                    body: JSON.stringify({ sql: sqlToExecute })
                });
            }

            let updatedAsset;
            
            // Use correct endpoint for edge functions
            if (selectedAsset.type === 'edge_function') {
                const deployResponse = await fetchWithAuth(`/api/data/${projectId}/edge-functions/deploy`, {
                    method: 'POST',
                    body: JSON.stringify({
                        name: detectedName,
                        content: editorSql,
                        env_vars: envVars,
                        imports: edgeImports,
                        timeout_ms: timeoutSec * 1000
                    })
                });
                
                // Transform deploy response to Asset structure
                updatedAsset = {
                    ...selectedAsset,
                    name: detectedName,
                    metadata: {
                        ...selectedAsset.metadata,
                        notes,
                        sql: editorSql,
                        env_vars: envVars,
                        imports: edgeImports,
                        timeout_ms: timeoutSec * 1000
                    }
                };
            } else {
                updatedAsset = await fetchWithAuth(`/api/data/${projectId}/assets`, {
                    method: 'POST',
                    body: JSON.stringify({
                        ...selectedAsset,
                        id: selectedAsset.isUnmanaged ? undefined : selectedAsset.id,
                        name: detectedName,
                        type: selectedAsset.type,
                        parent_id: selectedAsset.parent_id, // Preserve Parent ID
                        metadata: {
                            ...selectedAsset.metadata,
                            notes,
                            sql: editorSql,
                            env_vars: selectedAsset.type === 'edge_function' ? envVars : undefined,
                            imports: selectedAsset.type === 'edge_function' ? edgeImports : undefined,
                            timeout_ms: selectedAsset.type === 'edge_function' ? timeoutSec * 1000 : undefined,
                            schedule: selectedAsset.type === 'cron' ? cronSchedule : undefined
                        }
                    })
                });
            }

            setAssets(prev => prev.map(a => (a.id === selectedAsset.id ? updatedAsset : a)));
            setSelectedAsset(updatedAsset);
            setOriginalSql(editorSql);
            setIsDirty(false);
            setShowConflictModal(false); // Close modal if open
            setSuccessMsg(`Saved "${detectedName}"`);
            setTimeout(() => setSuccessMsg(null), 2000);
            fetchData();
        } catch (err: any) {
            setError(humanizePostgresError(err));
        }
        finally { setExecuting(false); }
    };

    const handleCreateAsset = async (defaultName: string, type: AssetType, parentId: string | null = null) => {
        const name = window.prompt(`Nome para o novo ${type === 'edge_function' ? 'Edge Function' : type}:`, defaultName);
        if (!name) return;

        try {
            let newAsset;
            
            // Use correct endpoint for edge functions
            if (type === 'edge_function') {
                // Create edge function with template content
                const template = `// Edge Function: ${name}
// Available Globals: crypto, $db, $fetch, env
// req = { body, query, headers, user }

export default async function(req) {
  const id = crypto.randomUUID();
  const apiKey = env.OPENAI_KEY || 'default_key';

  // Example $fetch (asynchronous request):
  // const response = await $fetch('https://httpbin.org/json');
  // console.log(response.status, response.data);

  return {
    id,
    message: \`Hello from Edge Engine!\`,
    key_status: apiKey ? 'Found' : 'Missing',
  };
}`;
                
                const deployResponse = await fetchWithAuth(`/api/data/${projectId}/edge-functions/deploy`, {
                    method: 'POST',
                    body: JSON.stringify({
                        name: name,
                        content: template,
                        env_vars: {},
                        imports: [],
                        timeout_ms: 5000
                    })
                });
                
                // Transform response to match expected Asset structure
                newAsset = {
                    id: `edge_func_${name}`,
                    name: name,
                    type: 'edge_function',
                    parent_id: parentId,
                    isUnmanaged: true,
                    isFromDatabase: true,
                    metadata: {
                        sql: template,
                        source: 'database',
                        env_vars: {},
                        imports: [],
                        timeout_ms: 5000
                    }
                };
            } else {
                newAsset = await fetchWithAuth(`/api/data/${projectId}/assets`, {
                    method: 'POST',
                    body: JSON.stringify({ name, type, parent_id: parentId })
                });
            }

            setAssets(prev => [...prev, newAsset]);

            if (type !== 'folder') {
                setSelectedAsset(newAsset);
                let template = '';
                if (type === 'edge_function') {
                    // Template already applied during creation, use existing content
                    template = newAsset.metadata?.sql || '';
                    setEnvVars(newAsset.metadata?.env_vars || {});
                    setEdgeImports(newAsset.metadata?.imports || []);
                    setTimeoutSec((newAsset.metadata?.timeout_ms || 5000) / 1000);
                } else if (type === 'cron') {
                    template = `-- Cron Job: ${name}\n-- Schedule: * * * * *\n\nSELECT 1;`;
                    setCronSchedule('0 * * * *');
                } else if (type === 'snippet') {
                    template = `-- Snippet: ${name}
-- This is a stored SQL snippet (not a database object)
-- Use this for large scripts, migrations, or reference code

-- Write your SQL here...`;
                } else {
                    template = `CREATE OR REPLACE FUNCTION ${name}(param_a text)\nRETURNS jsonb AS $$\nBEGIN\n  RETURN jsonb_build_object('echo', param_a);\nEND;\n$$ LANGUAGE plpgsql;`;
                }
                setEditorSql(template);
                setOriginalSql(template);
            } else if (parentId) {
                setExpandedFolders(prev => new Set(prev).add(parentId));
            }
            setSuccessMsg(`${type.toUpperCase()} created.`);
            setTimeout(() => setSuccessMsg(null), 2000);
        } catch (err: any) { setError(humanizePostgresError(err)); }
    };

    const executeTest = async () => {
        if (!selectedAsset) return;
        setExecuting(true);
        setTestResult(null);
        setExecutionLogs([]);
        setError(null);
        setActiveBottomTab('result');

        try {
            let payload = {};
            try { payload = JSON.parse(testParams); } catch (e) { throw new Error("Invalid JSON parameters"); }

            setExecutionLogs(prev => [...prev, `[${new Date().toLocaleTimeString()}] Starting execution of ${selectedAsset.name}...`]);
            setExecutionLogs(prev => [...prev, `[${new Date().toLocaleTimeString()}] Payload: ${JSON.stringify(payload)}`]);

            if (selectedAsset.type === 'edge_function') {
                const result = await fetchWithAuth(`/api/data/${projectId}/edge/${selectedAsset.name}`, {
                    method: 'POST',
                    body: JSON.stringify(payload)
                });
                setTestResult(result);
            } else if (selectedAsset.type === 'rpc') {
                const result = await fetchWithAuth(`/api/data/${projectId}/rpc/${selectedAsset.name}`, {
                    method: 'POST',
                    body: JSON.stringify(payload)
                });
                setTestResult(result);
            }
            setExecutionLogs(prev => [...prev, `[${new Date().toLocaleTimeString()}] Execution completed successfully.`]);
            setSuccessMsg("Executed");
            setTimeout(() => setSuccessMsg(null), 1000);
        } catch (e: any) {
            setError(humanizePostgresError(e));
            setTestResult({ error: e.message });
            setExecutionLogs(prev => [...prev, `[${new Date().toLocaleTimeString()}] ERROR: ${e.message}`]);
        } finally {
            setExecuting(false);
        }
    };

    // --- SAFE COPY CLIPBOARD (HTTP Support) ---
    const copyToClipboard = async (text: string) => {
        try {
            if (navigator.clipboard && window.isSecureContext) {
                await navigator.clipboard.writeText(text);
            } else {
                // Fallback for HTTP (Unsecure context)
                const textArea = document.createElement("textarea");
                textArea.value = text;
                textArea.style.position = "fixed";
                textArea.style.left = "-99999px";
                textArea.style.top = "0";
                document.body.appendChild(textArea);
                textArea.focus();
                textArea.select();
                return new Promise<void>((resolve, reject) => {
                    document.execCommand('copy') ? resolve() : reject();
                    textArea.remove();
                });
            }
            setSuccessMsg("Copied to clipboard!");
            setTimeout(() => setSuccessMsg(null), 2000);
        } catch (err) {
            setError("Failed to copy");
        }
    };

    const generateCurlCommand = () => {
        if (!selectedAsset) return '';
        const activeBranch = currentEnv || localStorage.getItem('cascata_env') || 'live';
        const branchPath = activeBranch !== 'live' ? `/branch/${activeBranch}` : '';
        const url = `${window.location.origin}/api/data/${projectId}${branchPath}/${selectedAsset.type === 'edge_function' ? 'edge' : 'rpc'}/${selectedAsset.name}`;
        const anonKey = projectKeys?.anon || 'YOUR_ANON_KEY';

        // Clean JSON payload for CLI
        const safePayload = testParams.replace(/'/g, "'\\''");

        // FIXED: Using 'apikey' header standard
        return `curl -X POST "${url}" \\
  -H "apikey: ${anonKey}" \\
  -H "Content-Type: application/json" \\
  -d '${safePayload}'`;
    };

    const handleSmartCopy = () => {
        if (!selectedAsset) return;

        const activeBranch = currentEnv || localStorage.getItem('cascata_env') || 'live';
        const branchPath = activeBranch !== 'live' ? `/branch/${activeBranch}` : '';

        if (selectedAsset.type === 'rpc') {
            // Copy full cURL
            copyToClipboard(generateCurlCommand());
        } else {
            // Edge Function or other: Copy just URL
            const type = selectedAsset.type === 'edge_function' ? 'edge' : 'rpc';
            const url = `${window.location.origin}/api/data/${projectId}${branchPath}/${type}/${selectedAsset.name}`;
            copyToClipboard(url);
        }
    };

    // --- GLOBAL SECRETS MANAGEMENT ---
    const addSecret = async () => {
        if (!newSecretKey || !newSecretVal) return;
        const key = newSecretKey.toUpperCase().replace(/[^A-Z0-9_]/g, '_');
        try {
            const res = await fetchWithAuth(`/api/control/projects/${projectId}/vault`, {
                method: 'POST',
                body: JSON.stringify({ name: key, type: 'string', value: newSecretVal, description: 'Environment Variable' })
            });
            setGlobalSecrets(prev => [...prev, res]);
            setSuccessMsg("Variável salva no Secure Vault.");
            setTimeout(() => setSuccessMsg(null), 2000);
            setNewSecretKey('');
            setNewSecretVal('');
        } catch (e: any) { setError(humanizePostgresError(e)); }
    };

    const removeSecret = async (id: string, key: string) => {
        try {
            await fetchWithAuth(`/api/control/projects/${projectId}/vault/${id}`, { method: 'DELETE' });
            setGlobalSecrets(prev => prev.filter(s => s.id !== id));
            setRevealedSecrets(prev => { const next = { ...prev }; delete next[id]; return next; });
        } catch (e: any) { setError(humanizePostgresError(e)); }
    };

    // --- UTILS ---
    const extractObjectName = (sql: string): string | null => {
        const match = sql.match(/CREATE\s+(?:OR\s+REPLACE\s+)?(?:FUNCTION|TRIGGER|VIEW|PROCEDURE)\s+(?:public\.)?(\w+)/i);
        return match ? match[1] : null;
    };

    const parseSqlArguments = (sql: string) => {
        const match = sql.match(/FUNCTION\s+(?:public\.)?[\w_]+\s*\((.*?)\)/is);
        if (!match || !match[1]) return [];

        return match[1].split(',').map(arg => {
            const parts = arg.trim().split(/\s+/);
            // Simplified parsing: name is first, type is rest
            const name = parts[0];
            const type = parts.slice(1).join(' ').toLowerCase();
            return { name, type };
        }).filter(a => a.name);
    };

    // --- SMART AUTO-FORM ---
    const handleFormChange = (key: string, value: any, type: string) => {
        try {
            const current = JSON.parse(testParams);

            let typedValue = value;

            // Type Intelligence
            if (type.includes('bool')) {
                // Boolean is already passed as true/false from toggle, just ensure it stays that way
                typedValue = value === true;
            } else if (type.includes('int') || type.includes('numeric') || type.includes('float') || type.includes('double')) {
                // Convert to number if valid, else keep string (to avoid NaN in UI during typing)
                const num = Number(value);
                if (!isNaN(num) && value !== '') typedValue = num;
                else typedValue = value; // Keep partial string inputs like "1." or "-"
            }
            // Date/Time/String/JSON stay as strings mainly, 
            // but could parse JSON if we had a dedicated JSON editor.

            current[key] = typedValue;
            setTestParams(JSON.stringify(current, null, 2));
        } catch (e) {
            // If JSON is broken, reset to simple object
            setTestParams(JSON.stringify({ [key]: value }, null, 2));
        }
    };

    const getTypedValue = (key: string) => {
        try {
            const current = JSON.parse(testParams);
            return current[key] ?? '';
        } catch (e) { return ''; }
    };

    // --- PERSISTENT DRAG & DROP FIX ---
    const isAncestor = (potentialAncestorId: string, targetId: string | null): boolean => {
        if (!targetId) return false;
        if (potentialAncestorId === targetId) return true;
        const target = assets.find(a => a.id === targetId);
        if (!target || !target.parent_id) return false;
        return isAncestor(potentialAncestorId, target.parent_id);
    };

    const handleDrop = async (e: React.DragEvent, targetFolderId: string | null) => {
        e.preventDefault();
        e.stopPropagation();
        setDragOverItem(null);

        const droppedId = e.dataTransfer.getData('text/plain');

        if (!droppedId) return;
        if (droppedId === targetFolderId) return; // Cannot drop into itself
        if (targetFolderId && isAncestor(droppedId, targetFolderId)) {
            setError("Cannot move a folder into its own child.");
            setTimeout(() => setError(null), 3000);
            return;
        }

        setExecuting(true);
        try {
            // 1. Find the Asset
            const assetToMove = assets.find(a => a.id === droppedId);
            if (!assetToMove) throw new Error("Asset not found");

            // 2. Prevent redundant update
            if (assetToMove.parent_id === targetFolderId) {
                setExecuting(false);
                return;
            }

            // 3. Optimistic Update
            setAssets(prev => prev.map(a => a.id === droppedId ? { ...a, parent_id: targetFolderId } : a));

            if (targetFolderId) {
                setExpandedFolders(prev => new Set(prev).add(targetFolderId));
            }

            // 4. API Call to Persist (Fixed parent_id logic)
            // Ensure null is sent for root, not undefined or string 'root'
            const safeParentId = targetFolderId || null;

            await fetchWithAuth(`/api/data/${projectId}/assets`, {
                method: 'POST',
                body: JSON.stringify({
                    id: assetToMove.id,
                    name: assetToMove.name,
                    type: assetToMove.type,
                    metadata: assetToMove.metadata || {},
                    parent_id: safeParentId
                })
            });

            setSuccessMsg(targetFolderId ? "Moved to folder" : "Moved to root");
            setTimeout(() => setSuccessMsg(null), 1500);
        } catch (e: any) {
            setError(humanizePostgresError(e));
            fetchData(); // Revert on error
        } finally {
            setExecuting(false);
        }
    };

    const handleRenameConfirm = async () => {
        if (!targetAsset || !renameValue) return;
        setExecuting(true);
        try {
            const oldName = targetAsset.name;
            const newName = renameValue;

            let updatedSql = targetAsset.metadata?.sql || '';

            if (updatedSql && ['rpc', 'trigger'].includes(targetAsset.type)) {
                const regex = new RegExp(`(CREATE\\s+(?:OR\\s+REPLACE\\s+)?(?:FUNCTION|TRIGGER)\\s+)(?:public\\.)?${oldName}`, 'i');
                if (regex.test(updatedSql)) {
                    updatedSql = updatedSql.replace(regex, `$1${newName}`);
                }
            }

            // FIXED: Explicitly preserve parent_id to avoid reset
            const safeParentId = targetAsset.parent_id || null;

            await fetchWithAuth(`/api/data/${projectId}/assets`, {
                method: 'POST',
                body: JSON.stringify({
                    id: targetAsset.id,
                    name: newName,
                    type: targetAsset.type,
                    metadata: { ...targetAsset.metadata, sql: updatedSql },
                    parent_id: safeParentId
                })
            });

            if (['rpc', 'trigger'].includes(targetAsset.type)) {
                const dropType = targetAsset.type === 'rpc' ? 'FUNCTION' : 'TRIGGER';
                const dropSql = `DROP ${dropType} IF EXISTS ${oldName} CASCADE;`;
                await fetchWithAuth(`/api/data/${projectId}/query`, {
                    method: 'POST',
                    body: JSON.stringify({ sql: dropSql })
                }).catch(() => { });

                if (updatedSql) {
                    await fetchWithAuth(`/api/data/${projectId}/query`, {
                        method: 'POST',
                        body: JSON.stringify({ sql: updatedSql })
                    });
                }
            }

            setAssets(prev => prev.map(a => a.id === targetAsset.id ? { ...a, name: newName, metadata: { ...a.metadata, sql: updatedSql } } : a));

            if (selectedAsset?.id === targetAsset.id) {
                setSelectedAsset(prev => prev ? { ...prev, name: newName, metadata: { ...prev.metadata, sql: updatedSql } } : null);
                setEditorSql(updatedSql);
            }

            setSuccessMsg("Renamed successfully.");
            setTimeout(() => setSuccessMsg(null), 2000);
            setShowRenameModal(false);
        } catch (e: any) {
            setError(humanizePostgresError(e));
        } finally {
            setExecuting(false);
        }
    };

    const handleDeleteConfirm = async () => {
        if (!targetAsset) return;
        setExecuting(true);
        try {
            // 22P02 FIX: Only call Asset DELETE API if it has a real UUID
            // Native functions start with 'native_' and are not in system.assets table with UUID
            const isNative = targetAsset.id.startsWith('native_');
            const isEdgeFunction = targetAsset.type === 'edge_function';

            if (!isNative && !isEdgeFunction) {
                await fetchWithAuth(`/api/data/${projectId}/assets/${targetAsset.id}`, { method: 'DELETE' });
            }

            // Handle edge function deletion via dedicated endpoint
            if (isEdgeFunction) {
                await fetchWithAuth(`/api/data/${projectId}/edge-functions/${targetAsset.name}`, { method: 'DELETE' });
            }

            // ALWAYS Try to drop the SQL object
            if (['rpc', 'trigger'].includes(targetAsset.type)) {
                const dropType = targetAsset.type === 'rpc' ? 'FUNCTION' : 'TRIGGER';
                await fetchWithAuth(`/api/data/${projectId}/query`, {
                    method: 'POST',
                    body: JSON.stringify({ sql: `DROP ${dropType} IF EXISTS ${targetAsset.name} CASCADE;` })
                }).catch(() => { });
            }

            setAssets(prev => prev.filter(a => a.id !== targetAsset.id));
            if (selectedAsset?.id === targetAsset.id) setSelectedAsset(null);
            setSuccessMsg("Asset deleted.");
            setTimeout(() => setSuccessMsg(null), 2000);
            setShowDeleteModal(false);
        } catch (e: any) {
            setError(humanizePostgresError(e));
        } finally {
            setExecuting(false);
        }
    };

    // --- SYNC FROM DATABASE ---
    const openSyncModal = async () => {
        setSyncLoading(true);
        setShowSyncModal(true);
        try {
            const [functionsData, triggersData, assetsData] = await Promise.all([
                fetchWithAuth(`/api/data/${projectId}/functions`),
                fetchWithAuth(`/api/data/${projectId}/triggers`),
                fetchWithAuth(`/api/data/${projectId}/assets`)
            ]);
            
            const managedNames = new Set(assetsData.map((a: any) => a.name));
            
            const items: {name: string, type: 'rpc' | 'trigger', checked: boolean, existing: boolean, ddl?: string, table_name?: string}[] = [];
            
            functionsData.forEach((fn: any) => {
                const name = fn.name;
                if (name && !IGNORED_PREFIXES.some(p => name.startsWith(p))) {
                    items.push({
                        name,
                        type: 'rpc',
                        checked: !managedNames.has(name),
                        existing: managedNames.has(name),
                        ddl: fn.ddl
                    });
                }
            });
            
            triggersData.forEach((tr: any) => {
                const name = tr.name;
                if (name) {
                    items.push({
                        name,
                        type: 'trigger',
                        checked: !managedNames.has(name),
                        existing: managedNames.has(name),
                        ddl: tr.ddl,
                        table_name: tr.table_name
                    });
                }
            });
            
            setSyncItems(items.sort((a, b) => a.name.localeCompare(b.name)));
        } catch (err: any) {
            setError(humanizePostgresError(err));
        } finally {
            setSyncLoading(false);
        }
    };

    const executeSync = async () => {
        const selectedItems = syncItems.filter((i: {checked: boolean}) => i.checked);
        if (selectedItems.length === 0) return;
        
        setSyncLoading(true);
        try {
            for (const item of selectedItems) {
                // Create asset with the DDL directly from the list (no extra API call needed)
                await fetchWithAuth(`/api/data/${projectId}/assets`, {
                    method: 'POST',
                    body: JSON.stringify({
                        name: item.name,
                        type: item.type,
                        parent_id: null,
                        metadata: {
                            sql: (item as any).ddl,
                            source: 'database',
                            table_name: (item as any).table_name
                        }
                    })
                });
            }
            
            setSuccessMsg(`Imported ${selectedItems.length} items from database`);
            setTimeout(() => setSuccessMsg(null), 3000);
            setShowSyncModal(false);
            fetchData();
        } catch (err: any) {
            setError(humanizePostgresError(err));
        } finally {
            setSyncLoading(false);
        }
    };

    const handleMoveConfirm = async () => {
        if (!targetAsset) return;

        // FIXED: Convert 'root' string to NULL
        const folderId = moveTargetFolder === 'root' ? null : moveTargetFolder;

        if (targetAsset.type === 'folder' && isAncestor(targetAsset.id, folderId)) {
            setError("Cannot move folder into its own child.");
            return;
        }

        setExecuting(true);
        try {
            await fetchWithAuth(`/api/data/${projectId}/assets`, {
                method: 'POST',
                body: JSON.stringify({
                    id: targetAsset.id,
                    name: targetAsset.name,
                    type: targetAsset.type,
                    metadata: targetAsset.metadata || {},
                    parent_id: folderId
                })
            });

            setAssets(prev => prev.map(a => a.id === targetAsset.id ? { ...a, parent_id: folderId } : a));
            setSuccessMsg("Asset moved.");
            setTimeout(() => setSuccessMsg(null), 2000);
            setShowMoveModal(false);
        } catch (e: any) {
            setError(humanizePostgresError(e));
        } finally {
            setExecuting(false);
        }
    };

    // --- RENDER HELPERS ---
    const renderTreeItem = (item: AssetTreeNode) => {
        const isFolder = item.type === 'folder';
        const isExpanded = expandedFolders.has(item.id);
        const isSelected = selectedAsset?.id === item.id;
        return (
            <div
                key={item.id}
                draggable={!item.isUnmanaged}
                onDragStart={(e) => {
                    // Important: Mark event so we know where it started
                    e.stopPropagation();
                    e.dataTransfer.setData('text/plain', item.id);
                    setDraggedItem(item.id);
                }}
                onDragOver={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    if (item.type === 'folder' && item.id !== draggedItem) setDragOverItem(item.id);
                }}
                onDragLeave={(e) => {
                    e.stopPropagation();
                    setDragOverItem(null);
                }}
                onDrop={(e) => {
                    // Strict Drop Handling for Folder Items
                    e.preventDefault();
                    e.stopPropagation();
                    if (item.type === 'folder') handleDrop(e, item.id);
                }}
            >
                <div
                    onClick={() => {
                        if (isFolder) {
                            const next = new Set(expandedFolders);
                            if (next.has(item.id)) next.delete(item.id); else next.add(item.id);
                            setExpandedFolders(next);
                        } else {
                            setSelectedAsset(item);
                            loadDefinition(item);
                            setNotes(item.metadata?.notes || '');
                            setEnvVars(item.metadata?.env_vars || {});
                            setEdgeImports(item.metadata?.imports || []);
                            setTimeoutSec((item.metadata?.timeout_ms || 5000) / 1000);
                            setCronSchedule(item.metadata?.schedule || '* * * * *');
                            setTestResult(null);
                            setExecutionLogs([]);
                        }
                    }}
                    onContextMenu={(e) => { e.preventDefault(); e.stopPropagation(); setContextMenu({ x: e.clientX, y: e.clientY, item }); }}
                    className={`flex items-center gap-2 px-3 py-2 rounded-lg cursor-pointer transition-all border border-transparent mb-0.5 ${isSelected ? 'bg-indigo-600 text-white font-medium shadow-md' : 'text-slate-600 hover:bg-slate-100'} ${dragOverItem === item.id ? 'bg-indigo-100 border-indigo-300 ring-2 ring-indigo-300' : ''}`}
                >
                    {isFolder ? (isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />) : null}
                    {isFolder ? <Folder size={16} className={isSelected ? 'text-white' : 'text-indigo-400'} /> : 
                     item.type === 'cron' ? <Clock size={16} className={isSelected ? 'text-white' : 'text-amber-500'} /> :
                     item.type === 'snippet' ? <FileText size={16} className={isSelected ? 'text-white' : 'text-emerald-400'} /> :
                     item.isUnmanaged && item.type === 'rpc' ? <Code2 size={16} className={isSelected ? 'text-white' : 'text-blue-500'} /> :
                     item.isUnmanaged && item.type === 'trigger' ? <Zap size={16} className={isSelected ? 'text-white' : 'text-yellow-500'} /> :
                     item.isUnmanaged ? <Ghost size={16} /> : 
                     item.metadata?.source === 'database' ? <GitCompare size={16} className="text-emerald-500" /> :
                     <FileCode size={16} />}
                    <span className="text-xs truncate">{item.name}</span>
                </div>
                {isFolder && isExpanded && <div className="pl-4 ml-2 border-l border-slate-200">{item.children.map(renderTreeItem)}</div>}
            </div>
        );
    };

    const treeData = useMemo(() => {
        const buildTree = (parentId: string | null): AssetTreeNode[] => {
            return assets
                .filter(a => {
                    const isRelevantType = a.type === 'folder' || activeContext === 'all' || a.type === activeContext;
                    const matchesParent = parentId === null
                        ? (!a.parent_id)
                        : (a.parent_id === parentId);
                    return isRelevantType && matchesParent;
                })
                .map(a => ({
                    ...a,
                    children: a.type === 'folder' ? buildTree(a.id) : []
                }));
        };
        return buildTree(null);
    }, [assets, activeContext]);

    const allFolders = useMemo(() => assets.filter(a => a.type === 'folder'), [assets]);

    return (
        <div className="flex h-full bg-[#FAFAFA] text-slate-900 font-sans overflow-hidden">
            {successMsg && <div className="fixed top-6 left-1/2 -translate-x-1/2 z-[100] bg-emerald-600 text-white px-6 py-3 rounded-full shadow-2xl flex items-center gap-3 animate-in slide-in-from-top-4"><CheckCircle2 size={18} /><span className="text-xs font-bold">{successMsg}</span></div>}
            {error && <div className="fixed top-6 left-1/2 -translate-x-1/2 z-[100] bg-rose-600 text-white px-6 py-3 rounded-full shadow-2xl flex items-center gap-3 cursor-pointer" onClick={() => setError(null)}><AlertCircle size={18} /><span className="text-xs font-bold">{error}</span></div>}

            {/* SIDEBAR */}
            <aside
                className={`w-72 bg-white border-r border-slate-200 flex flex-col shrink-0 z-20 transition-colors ${dragOverItem === 'root' ? 'bg-indigo-50 border-indigo-300 ring-2 ring-inset ring-indigo-300' : ''}`}
                onDragOver={(e) => {
                    e.preventDefault();
                    // If dragging over sidebar empty space, mark root as target
                    if (!dragOverItem) setDragOverItem('root');
                }}
                onDragLeave={(e) => {
                    // Prevent flickering when leaving sidebar to children items
                    if (e.currentTarget.contains(e.relatedTarget as Node)) return;
                    setDragOverItem(null);
                }}
                onDrop={(e) => {
                    // Drop on the sidebar background = Drop to Root
                    handleDrop(e, null);
                    setDragOverItem(null);
                }}
            >
                <div className="p-4 border-b border-slate-100">
                    <h2 className="text-sm font-black text-slate-900 flex items-center gap-2 mb-4"><Cpu size={18} className="text-indigo-600" /> Logic Engine</h2>
                    <div className="flex bg-slate-100 p-1 rounded-lg mb-4">
                        {['all', 'rpc', 'trigger', 'snippet', 'cron', 'edge_function'].map((ctx) => (
                            <button key={ctx} onClick={() => { setActiveContext(ctx as any); setSelectedAsset(null); }} className={`flex-1 py-1.5 rounded-md flex justify-center items-center transition-all ${activeContext === ctx ? 'bg-white shadow text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`} title={ctx === 'all' ? 'All Assets' : ctx}>
                                {ctx === 'all' && <Layout size={14} />}{ctx === 'rpc' && <Code2 size={14} />}{ctx === 'trigger' && <Zap size={14} />}{ctx === 'snippet' && <FileText size={14} />}{ctx === 'cron' && <Clock size={14} />}{ctx === 'edge_function' && <Globe size={14} />}
                            </button>
                        ))}
                    </div>
                    <div className="flex gap-2 mb-3">
                        <button onClick={() => handleCreateAsset('new_folder', 'folder')} className="flex-1 py-2 border border-slate-200 rounded-lg text-[10px] font-bold text-slate-500 hover:bg-slate-50 flex items-center justify-center gap-1"><FolderPlus size={12} /> Folder</button>
                        <button onClick={() => handleCreateAsset('new_' + activeContext, activeContext === 'all' ? 'snippet' : activeContext)} className="flex-[2] py-2 bg-indigo-600 text-white rounded-lg text-[10px] font-bold flex items-center justify-center gap-1 hover:bg-indigo-700 shadow-sm"><Plus size={12} /> New {activeContext === 'all' ? 'Snippet' : activeContext}</button>
                    </div>

                    {/* Sync from Database Button */}
                    <button onClick={openSyncModal} className="w-full py-2 bg-emerald-50 text-emerald-700 rounded-lg text-[10px] font-bold flex items-center justify-center gap-2 hover:bg-emerald-100 transition-all border border-emerald-100 mb-3">
                        <GitCompare size={12} /> Sync from Database
                    </button>

                    {/* Global Vars Button */}
                    <button onClick={() => setShowSecretsModal(true)} className="w-full py-2 bg-amber-50 text-amber-700 rounded-lg text-[10px] font-bold flex items-center justify-center gap-2 hover:bg-amber-100 transition-all border border-amber-100">
                        <Terminal size={12} /> Global Variables (Env)
                    </button>
                </div>
                <div className="flex-1 overflow-y-auto p-3 min-h-0">
                    {treeData.map(renderTreeItem)}
                    {/* Empty state zone for easier dropping */}
                    {treeData.length === 0 && <div className="h-20 flex items-center justify-center text-slate-300 text-xs italic border-2 border-dashed border-slate-100 rounded-xl mt-2">Drop items here</div>}
                </div>
            </aside>

            {/* MAIN EDITOR AREA */}
            <main className="flex-1 flex flex-col min-w-0 relative">
                {selectedAsset ? (
                    <>
                        {/* TOOLBAR */}
                        <header className="h-14 bg-white border-b border-slate-200 flex items-center justify-between px-6 shrink-0">
                            <div className="flex items-center gap-3">
                                <div className={`p-1.5 rounded-lg ${isDirty ? 'bg-amber-100 text-amber-600' : 'bg-slate-100 text-slate-500'}`}>
                                    {selectedAsset.type === 'edge_function' ? <Globe size={16} /> : 
                                     selectedAsset.type === 'cron' ? <Clock size={16} /> : 
                                     selectedAsset.type === 'trigger' ? <Zap size={16} /> :
                                     selectedAsset.type === 'snippet' ? <FileText size={16} /> :
                                     <Code2 size={16} />}
                                </div>
                                <span className="font-mono text-sm font-bold text-slate-700">{selectedAsset.name}</span>
                                {isDirty && <span className="text-[9px] bg-amber-500 text-white px-2 py-0.5 rounded-full font-bold">UNSAVED</span>}
                                {selectedAsset.isUnmanaged && <span className="text-[9px] bg-slate-200 text-slate-600 px-2 py-0.5 rounded-full font-bold">NATIVE</span>}
                                {selectedAsset.metadata?.source === 'database' && <span className="text-[9px] bg-emerald-100 text-emerald-600 px-2 py-0.5 rounded-full font-bold">FROM DB</span>}
                                {selectedAsset.type === 'snippet' && <span className="text-[9px] bg-purple-100 text-purple-600 px-2 py-0.5 rounded-full font-bold">SNIPPET</span>}
                            </div>
                            <div className="flex items-center gap-2">
                                {/* Copy URL Button (Intelligent) */}
                                <button onClick={handleSmartCopy} className="p-2 text-slate-400 hover:text-emerald-600 hover:bg-emerald-50 rounded-lg transition-all flex items-center gap-2 text-xs font-bold" title="Copy Invocation Command">
                                    <LinkIcon size={16} />
                                    <span className="hidden lg:inline">{selectedAsset.type === 'rpc' ? 'Copy cURL' : 'Copy URL'}</span>
                                </button>

                                {/* Status Toggle Button (Play/Stop - Active/Inactive) */}
                                {selectedAsset && (
                                    <button
                                        onClick={handleToggleStatus}
                                        disabled={executing}
                                        className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-bold transition-all border ${
                                            selectedAsset.metadata?.status !== 'inactive'
                                                ? 'bg-emerald-50 border-emerald-200 text-emerald-600 hover:bg-emerald-100 hover:border-emerald-300 shadow-sm' 
                                                : 'bg-rose-50 border-rose-200 text-rose-600 hover:bg-rose-100 hover:border-rose-300 shadow-sm'
                                        }`}
                                        title={selectedAsset.metadata?.status !== 'inactive' ? "Pause/Deactivate Asset" : "Start/Activate Asset"}
                                    >
                                        {selectedAsset.metadata?.status !== 'inactive' ? (
                                            <Play size={12} className="fill-emerald-600 text-emerald-600 animate-pulse" />
                                        ) : (
                                            <X size={12} className="text-rose-600 stroke-[3px]" />
                                        )}
                                        <span>{selectedAsset.metadata?.status !== 'inactive' ? 'Active' : 'Inactive'}</span>
                                    </button>
                                )}

                                <div className="w-[1px] h-6 bg-slate-200 mx-2"></div>

                                {/* History Dropdown/Modal Trigger */}
                                <div className="relative">
                                    {selectedAsset.type === 'edge_function' ? (
                                        <button 
                                            onClick={() => setShowHistoryModal(true)} 
                                            className="p-2 text-slate-400 hover:text-indigo-600 hover:bg-indigo-50 rounded-lg transition-all" 
                                            title="Version History & Diff"
                                        >
                                            <History size={18} />
                                        </button>
                                    ) : (
                                        <>
                                            <button onClick={() => setShowHistoryDropdown(!showHistoryDropdown)} className="p-2 text-slate-400 hover:text-indigo-600 hover:bg-indigo-50 rounded-lg transition-all" title="Version History"><History size={18}/></button>
                                            {showHistoryDropdown && (
                                                <div className="absolute top-full right-0 mt-2 w-48 bg-white border border-slate-200 rounded-xl shadow-xl z-50 p-2 animate-in fade-in zoom-in-95">
                                                    <div className="text-[10px] font-bold text-slate-400 uppercase px-2 mb-2">Recent Versions</div>
                                                    <button className="w-full text-left px-3 py-2 hover:bg-slate-50 rounded-lg text-xs font-medium text-slate-600 flex justify-between"><span>Current</span> <span className="text-emerald-500 text-[9px]">ACTIVE</span></button>
                                                    <div className="border-t border-slate-100 my-1"></div>
                                                    <div className="text-center py-2 text-[10px] text-slate-300 italic">No history yet</div>
                                                </div>
                                            )}
                                        </>
                                    )}
                                </div>

                                <button onClick={() => { }} className="p-2 text-slate-400 hover:text-purple-600 hover:bg-purple-50 rounded-lg transition-all" title="Explain with AI"><Sparkles size={18} /></button>
                                <div className="w-[1px] h-6 bg-slate-200 mx-2"></div>
                                <button onClick={() => { setEditorSql(''); }} className="p-2 text-slate-400 hover:text-rose-600 rounded-lg" title="Clear"><Eraser size={18} /></button>
                                <button onClick={() => handleSaveObject()} disabled={!isDirty || executing} className={`flex items-center gap-2 px-4 py-2 rounded-lg text-xs font-bold transition-all ${isDirty ? 'bg-indigo-600 text-white hover:bg-indigo-700 shadow-md' : 'bg-slate-100 text-slate-400'}`}>
                                    {executing ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />} Save <span className="opacity-50 text-[9px] ml-1">CTRL+S</span>
                                </button>
                            </div>
                        </header>

                        {/* EDITOR + PANELS */}
                        <div className="flex-1 flex flex-col relative overflow-hidden bg-[#1e1e1e]">
                            {/* MONACO-LIKE EDITOR */}
                            <div className="flex-1 relative flex overflow-hidden">
                                {/* Line Numbers */}
                                <div ref={lineNumbersRef} className="w-12 bg-[#1e1e1e] border-r border-[#333] text-[#666] text-xs font-mono text-right pr-3 pt-4 select-none overflow-hidden leading-6">
                                    {editorSql.split('\n').map((_, i) => <div key={i}>{i + 1}</div>)}
                                </div>
                                {/* Code Area */}
                                <textarea
                                    ref={editorRef}
                                    value={editorSql}
                                    onChange={(e) => setEditorSql(e.target.value)}
                                    onScroll={handleEditorScroll}
                                    onKeyDown={(e) => {
                                        if (e.key === 'Tab') {
                                            e.preventDefault();
                                            const target = e.target as HTMLTextAreaElement;
                                            const start = target.selectionStart;
                                            const end = target.selectionEnd;
                                            const newValue = editorSql.substring(0, start) + "  " + editorSql.substring(end);
                                            setEditorSql(newValue);
                                            setTimeout(() => { target.selectionStart = target.selectionEnd = start + 2; }, 0);
                                        }
                                    }}
                                    className="flex-1 bg-[#1e1e1e] text-[#d4d4d4] font-mono text-sm leading-6 p-4 outline-none resize-none whitespace-pre border-none"
                                    spellCheck="false"
                                />
                                {/* EDGE MODULES FLOATING PANEL (Updated Info) */}
                                {selectedAsset.type === 'edge_function' && (
                                    <div className="absolute top-4 right-4 bg-[#252526] border border-[#333] rounded-lg p-3 w-64 shadow-xl z-10 opacity-90 hover:opacity-100 transition-opacity">
                                        <h4 className="text-[10px] font-bold text-[#888] uppercase mb-2 flex justify-between">Available Globals <Box size={12} /></h4>
                                        <div className="space-y-1 mb-2 max-h-36 overflow-y-auto">
                                            <div className="text-xs text-[#aaa] font-mono bg-[#333] px-2 py-1 rounded">crypto (Random/UUID)</div>
                                            <div className="text-xs text-[#aaa] font-mono bg-[#333] px-2 py-1 rounded">$fetch (Async HTTP)</div>
                                            <div className="text-xs text-[#aaa] font-mono bg-[#333] px-2 py-1 rounded">$db (Postgres Pool)</div>
                                            <div className="text-xs text-[#aaa] font-mono bg-[#333] px-2 py-1 rounded">atob / btoa (Base64)</div>
                                        </div>
                                        <div className="border-t border-[#333] mt-2 pt-2 text-[10px] text-[#888] font-mono">
                                            <div className="text-emerald-500 font-bold uppercase text-[9px] mb-1">Using $fetch:</div>
                                            <div className="text-[#bbb] bg-[#1e1e1e] p-1.5 rounded leading-normal text-[9px] overflow-x-auto">
                                                {`const { ok, status, data } = await $fetch(url, { method: 'POST', body: {...} });`}
                                            </div>
                                        </div>
                                    </div>
                                )}
                            </div>

                            {/* RESIZER HANDLE */}
                            <div
                                onMouseDown={() => setIsResizingPanel(true)}
                                className="h-1 bg-[#333] hover:bg-indigo-500 cursor-row-resize z-20 transition-colors flex items-center justify-center"
                            >
                                <GripHorizontal size={12} className="text-[#666]" />
                            </div>

                            {/* BOTTOM PANEL (Tabs: Output, Logs, Params, Form) */}
                            <div className="bg-white border-t border-slate-200 flex flex-col" style={{ height: bottomPanelHeight }}>
                                <div className="flex border-b border-slate-100 bg-slate-50">
                                    {/* Run Button */}
                                    <button onClick={executeTest} disabled={executing} className="bg-emerald-600 text-white px-6 py-3 text-xs font-black uppercase tracking-widest hover:bg-emerald-700 flex items-center gap-2">
                                        {executing ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />} RUN
                                    </button>

                                    {/* Tabs */}
                                    {[
                                        { id: 'form', label: 'Auto-Form', icon: Layout },
                                        { id: 'params', label: 'JSON Input', icon: Code2 },
                                        { id: 'result', label: 'Result', icon: MonitorPlay },
                                        { id: 'logs', label: 'Live Logs', icon: ScrollText }
                                    ].map(tab => (
                                        <button
                                            key={tab.id}
                                            onClick={() => setActiveBottomTab(tab.id as any)}
                                            className={`px-5 py-3 text-xs font-bold flex items-center gap-2 border-r border-slate-100 transition-colors ${activeBottomTab === tab.id ? 'bg-white text-indigo-600 border-t-2 border-t-indigo-600' : 'text-slate-500 hover:bg-slate-100'}`}
                                        >
                                            <tab.icon size={14} /> {tab.label}
                                        </button>
                                    ))}

                                    {/* EDITABLE TIMEOUT */}
                                    <div className="flex-1 flex justify-end items-center px-4 gap-2">
                                        {selectedAsset.type === 'edge_function' && (
                                            <div className="flex items-center gap-2 bg-slate-100 rounded-lg px-2 py-1">
                                                <span className="text-[10px] font-bold text-slate-400 uppercase">Timeout (s):</span>
                                                <input
                                                    type="number"
                                                    value={timeoutSec}
                                                    onChange={(e) => {
                                                        const val = parseInt(e.target.value);
                                                        setTimeoutSec(isNaN(val) ? 5 : val);
                                                        setIsDirty(true);
                                                    }}
                                                    className="w-10 bg-transparent text-xs font-black text-slate-600 outline-none text-right"
                                                />
                                            </div>
                                        )}
                                    </div>
                                </div>

                                <div className="flex-1 overflow-auto p-0 relative">
                                    {/* FORM VIEW (AUTO-GENERATED & SMART TYPES) */}
                                    {activeBottomTab === 'form' && (
                                        <div className="p-6">
                                            <div className="max-w-xl space-y-4">
                                                {parseSqlArguments(editorSql).length > 0 ? (
                                                    parseSqlArguments(editorSql).map((arg, idx) => (
                                                        <div key={idx} className="flex flex-col gap-1">
                                                            <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">{arg.name} <span className="text-indigo-400">({arg.type})</span></label>

                                                            {/* TYPE-AWARE INPUTS */}
                                                            {arg.type.includes('bool') ? (
                                                                <div className="flex items-center gap-3">
                                                                    <button
                                                                        onClick={() => handleFormChange(arg.name, !getTypedValue(arg.name), 'bool')}
                                                                        className={`w-12 h-6 rounded-full p-1 transition-colors ${getTypedValue(arg.name) === true ? 'bg-emerald-500' : 'bg-slate-200'}`}
                                                                    >
                                                                        <div className={`w-4 h-4 bg-white rounded-full shadow-md transition-transform ${getTypedValue(arg.name) === true ? 'translate-x-6' : ''}`}></div>
                                                                    </button>
                                                                    <span className="text-xs font-bold text-slate-600">{getTypedValue(arg.name) === true ? 'TRUE' : 'FALSE'}</span>
                                                                </div>
                                                            ) : (arg.type.includes('int') || arg.type.includes('numeric') || arg.type.includes('float')) ? (
                                                                <input
                                                                    type="number"
                                                                    value={getTypedValue(arg.name)}
                                                                    onChange={(e) => handleFormChange(arg.name, e.target.value, 'number')}
                                                                    className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-sm font-bold text-slate-700 outline-none focus:ring-2 focus:ring-indigo-500/20 transition-all font-mono"
                                                                    placeholder="0"
                                                                />
                                                            ) : (arg.type.includes('timestamp') || arg.type.includes('date')) ? (
                                                                <input
                                                                    type={arg.type.includes('timestamp') ? "datetime-local" : "date"}
                                                                    value={getTypedValue(arg.name)}
                                                                    onChange={(e) => handleFormChange(arg.name, e.target.value, 'date')}
                                                                    className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-sm font-bold text-slate-700 outline-none focus:ring-2 focus:ring-indigo-500/20 transition-all font-mono"
                                                                />
                                                            ) : (
                                                                <input
                                                                    value={getTypedValue(arg.name)}
                                                                    onChange={(e) => handleFormChange(arg.name, e.target.value, 'text')}
                                                                    placeholder={`Value for ${arg.name}`}
                                                                    className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-sm font-bold text-slate-700 outline-none focus:ring-2 focus:ring-indigo-500/20 transition-all"
                                                                />
                                                            )}
                                                        </div>
                                                    ))
                                                ) : (
                                                    <div className="text-center py-10 text-slate-400">
                                                        <FileText size={32} className="mx-auto mb-2 opacity-20" />
                                                        <p className="text-xs font-bold">No arguments detected in function signature.</p>
                                                        <button onClick={() => setActiveBottomTab('params')} className="text-indigo-600 text-xs font-bold hover:underline mt-2">Use JSON Mode</button>
                                                    </div>
                                                )}
                                            </div>
                                        </div>
                                    )}

                                    {/* JSON INPUT */}
                                    {activeBottomTab === 'params' && (
                                        <textarea
                                            value={testParams}
                                            onChange={(e) => setTestParams(e.target.value)}
                                            className="w-full h-full p-6 font-mono text-xs text-slate-700 bg-slate-50 outline-none resize-none"
                                            spellCheck="false"
                                        />
                                    )}

                                    {/* RESULTS */}
                                    {activeBottomTab === 'result' && (
                                        <div className="w-full h-full bg-[#0f172a] text-emerald-400 p-6 font-mono text-xs overflow-auto">
                                            {testResult ? (
                                                <pre>{JSON.stringify(testResult, null, 2)}</pre>
                                            ) : (
                                                <div className="h-full flex items-center justify-center text-slate-700">
                                                    <Terminal size={32} className="opacity-20" />
                                                </div>
                                            )}
                                        </div>
                                    )}

                                    {/* LOGS */}
                                    {activeBottomTab === 'logs' && (
                                        <div className="w-full h-full bg-white p-4 font-mono text-xs space-y-1 overflow-auto">
                                            {executionLogs.length === 0 && <p className="text-slate-300 italic">Waiting for execution...</p>}
                                            {executionLogs.map((log, i) => (
                                                <div key={i} className="border-b border-slate-50 pb-1 mb-1 text-slate-600">
                                                    <span className="text-indigo-400 mr-2">$</span>{log}
                                                </div>
                                            ))}
                                        </div>
                                    )}
                                </div>
                            </div>
                        </div>
                    </>
                ) : (
                    <div className="flex-1 flex flex-col items-center justify-center text-slate-300 bg-[#FAFAFA]">
                        <div className="w-24 h-24 bg-white rounded-[2rem] flex items-center justify-center shadow-lg mb-6">
                            <Cpu size={40} className="text-indigo-200" />
                        </div>
                        <h3 className="text-2xl font-black text-slate-400 tracking-tighter">Logic Workspace</h3>
                        <p className="text-xs font-bold uppercase tracking-widest mt-2 opacity-60">Select or create an asset to begin</p>
                    </div>
                )}
            </main>

            {/* CONTEXT MENUS & MODALS */}
            {contextMenu && (
                <div className="fixed z-50 bg-white border border-slate-200 shadow-xl rounded-xl p-1 w-56 animate-in fade-in zoom-in-95" style={{ top: contextMenu.y, left: contextMenu.x }}>
                    {contextMenu.item.type === 'folder' ? (
                        <>
                            <button onClick={() => { handleCreateAsset(`new_rpc`, 'rpc', contextMenu.item.id); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><Code2 size={12} /> New RPC</button>
                            <button onClick={() => { handleCreateAsset(`new_trigger`, 'trigger', contextMenu.item.id); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><Zap size={12} /> New Trigger</button>
                            <button onClick={() => { handleCreateAsset(`new_cron`, 'cron', contextMenu.item.id); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><Clock size={12} /> New Cron Job</button>
                            <button onClick={() => { handleCreateAsset(`new_edge`, 'edge_function', contextMenu.item.id); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><Globe size={12} /> New Edge Function</button>
                            <button onClick={() => { handleCreateAsset(`new_snippet`, 'snippet', contextMenu.item.id); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><FileText size={12} /> New Snippet</button>
                            <div className="h-[1px] bg-slate-100 my-1"></div>
                            <button onClick={() => { handleCreateAsset(`new_folder`, 'folder', contextMenu.item.id); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><FolderPlus size={12} /> New Folder</button>
                            <div className="h-[1px] bg-slate-100 my-1"></div>
                        </>
                    ) : (
                        <>
                            <button onClick={() => { setSelectedAsset(contextMenu.item); handleSmartCopy(); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><LinkIcon size={12} /> Copy {contextMenu.item.type === 'rpc' ? 'cURL' : 'URL'}</button>
                            <button onClick={() => { setTargetAsset(contextMenu.item); setShowMoveModal(true); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><Move size={12} /> Move to...</button>
                            <div className="h-[1px] bg-slate-100 my-1"></div>
                        </>
                    )}

                    <button onClick={() => { setTargetAsset(contextMenu.item); setShowRenameModal(true); setRenameValue(contextMenu.item.name); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-slate-600 hover:bg-slate-50 rounded-lg flex items-center gap-2"><Edit size={12} /> Rename</button>
                    <button onClick={() => { setTargetAsset(contextMenu.item); setShowDeleteModal(true); setContextMenu(null); }} className="w-full text-left px-3 py-2 text-xs font-bold text-rose-600 hover:bg-rose-50 rounded-lg flex items-center gap-2"><Trash2 size={12} /> Delete</button>
                </div>
            )}

            {/* RENAME MODAL */}
            {showRenameModal && (
                <div className="fixed inset-0 bg-slate-950/50 backdrop-blur-sm z-[60] flex items-center justify-center p-4">
                    <div className="bg-white rounded-2xl p-6 w-full max-w-sm shadow-2xl">
                        <h3 className="font-bold text-slate-900 mb-4">Rename Asset</h3>
                        <input autoFocus value={renameValue} onChange={e => setRenameValue(e.target.value)} className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-sm font-bold outline-none mb-4" />
                        <div className="flex gap-2">
                            <button onClick={() => setShowRenameModal(false)} className="flex-1 py-3 text-xs font-bold text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
                            <button onClick={handleRenameConfirm} className="flex-1 py-3 bg-indigo-600 text-white rounded-xl text-xs font-bold shadow-lg hover:bg-indigo-700">Confirm</button>
                        </div>
                    </div>
                </div>
            )}

            {/* MOVE MODAL */}
            {showMoveModal && (
                <div className="fixed inset-0 bg-slate-950/50 backdrop-blur-sm z-[60] flex items-center justify-center p-4">
                    <div className="bg-white rounded-2xl p-6 w-full max-w-sm shadow-2xl">
                        <h3 className="font-bold text-slate-900 mb-4">Move {targetAsset?.name}</h3>
                        <div className="space-y-2 mb-6">
                            <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Select Destination</label>
                            <select
                                value={moveTargetFolder}
                                onChange={(e) => setMoveTargetFolder(e.target.value)}
                                className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-sm font-bold outline-none"
                            >
                                <option value="root">/ (Root)</option>
                                {allFolders.filter(f => f.id !== targetAsset?.id).map(f => (
                                    <option key={f.id} value={f.id}>{f.name}</option>
                                ))}
                            </select>
                        </div>
                        <div className="flex gap-2">
                            <button onClick={() => setShowMoveModal(false)} className="flex-1 py-3 text-xs font-bold text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
                            <button onClick={handleMoveConfirm} className="flex-1 py-3 bg-indigo-600 text-white rounded-xl text-xs font-bold shadow-lg hover:bg-indigo-700">Move</button>
                        </div>
                    </div>
                </div>
            )}

            {/* CONFLICT MODAL */}
            {showConflictModal && conflictData && (
                <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[100] flex items-center justify-center p-8 animate-in zoom-in-95">
                    <div className="bg-white rounded-[3rem] w-full max-w-5xl h-[80vh] flex flex-col shadow-2xl border border-slate-200 overflow-hidden">
                        <header className="p-8 border-b border-slate-100 bg-amber-50/50 flex items-center justify-between">
                            <div className="flex items-center gap-4">
                                <div className="w-12 h-12 bg-amber-500 text-white rounded-2xl flex items-center justify-center shadow-lg"><AlertTriangle size={24} /></div>
                                <div>
                                    <h3 className="text-2xl font-black text-slate-900 tracking-tight">Function Overload Conflict</h3>
                                    <p className="text-xs text-amber-700 font-bold mt-1">A function with this name already exists but has different arguments.</p>
                                </div>
                            </div>
                            <button onClick={() => setShowConflictModal(false)} className="p-3 bg-white hover:bg-slate-100 rounded-full transition-all text-slate-400"><X size={20} /></button>
                        </header>

                        <div className="flex-1 flex overflow-hidden">
                            {/* OLD VERSION */}
                            <div className="flex-1 bg-slate-50 p-6 border-r border-slate-200 flex flex-col">
                                <div className="flex items-center justify-between mb-4">
                                    <span className="text-[10px] font-black text-slate-400 uppercase tracking-widest bg-white px-3 py-1 rounded-lg border border-slate-200">Current Database Version</span>
                                    <span className="text-xs font-mono font-bold text-slate-600">{conflictData.oldArgs}</span>
                                </div>
                                <pre className="flex-1 bg-white border border-slate-200 rounded-2xl p-6 font-mono text-xs text-slate-500 overflow-auto">{conflictData.oldCode}</pre>
                            </div>

                            {/* NEW VERSION */}
                            <div className="flex-1 bg-white p-6 flex flex-col">
                                <div className="flex items-center justify-between mb-4">
                                    <span className="text-[10px] font-black text-indigo-600 uppercase tracking-widest bg-indigo-50 px-3 py-1 rounded-lg border border-indigo-100">New Proposed Version</span>
                                    <span className="text-xs font-mono font-bold text-indigo-600">{conflictData.newArgs}</span>
                                </div>
                                <pre className="flex-1 bg-slate-900 text-emerald-400 rounded-2xl p-6 font-mono text-xs overflow-auto border border-slate-800 shadow-inner">{conflictData.newCode}</pre>
                            </div>
                        </div>

                        <footer className="p-8 border-t border-slate-100 bg-white flex justify-end gap-4">
                            <button onClick={() => setShowConflictModal(false)} className="px-8 py-4 rounded-2xl text-xs font-black text-slate-400 uppercase tracking-widest hover:bg-slate-50 transition-all">Cancel</button>
                            <button
                                onClick={() => handleSaveObject(true)} // Force Overwrite
                                className="px-8 py-4 bg-indigo-600 text-white rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-indigo-700 shadow-xl flex items-center gap-2 transition-all"
                            >
                                <GitCompare size={16} /> Overwrite (Drop Old & Create New)
                            </button>
                        </footer>
                    </div>
                </div>
            )}

            {/* SYNC FROM DATABASE MODAL */}
            {showSyncModal && (
                <div className="fixed inset-0 bg-slate-950/50 backdrop-blur-sm z-[70] flex items-center justify-center p-4">
                    <div className="bg-white rounded-2xl p-6 w-full max-w-2xl shadow-2xl max-h-[80vh] flex flex-col">
                        <div className="flex items-center justify-between mb-4">
                            <div className="flex items-center gap-3">
                                <div className="w-10 h-10 bg-emerald-100 text-emerald-600 rounded-xl flex items-center justify-center">
                                    <GitCompare size={20} />
                                </div>
                                <div>
                                    <h3 className="font-bold text-slate-900">Sync from Database</h3>
                                    <p className="text-xs text-slate-500">Import functions and triggers from PostgreSQL as editable snippets</p>
                                </div>
                            </div>
                            <button onClick={() => setShowSyncModal(false)} className="p-2 hover:bg-slate-100 rounded-lg text-slate-400"><X size={18} /></button>
                        </div>

                        {syncLoading ? (
                            <div className="flex-1 flex items-center justify-center py-12">
                                <Loader2 size={32} className="animate-spin text-indigo-600" />
                            </div>
                        ) : (
                            <>
                                <div className="flex-1 overflow-y-auto border border-slate-200 rounded-xl mb-4 max-h-[50vh]">
                                    {syncItems.length === 0 ? (
                                        <div className="p-8 text-center text-slate-400">
                                            <FileCode size={32} className="mx-auto mb-2 opacity-50" />
                                            <p className="text-xs">No functions or triggers found in database</p>
                                        </div>
                                    ) : (
                                        <div className="divide-y divide-slate-100">
                                            {syncItems.map((item, idx) => (
                                                <div key={idx} className={`flex items-center gap-3 px-4 py-3 hover:bg-slate-50 ${item.existing ? 'bg-amber-50/50' : ''}`}>
                                                    <input
                                                        type="checkbox"
                                                        checked={item.checked}
                                                        onChange={(e) => {
                                                            const newItems = [...syncItems];
                                                            newItems[idx].checked = e.target.checked;
                                                            setSyncItems(newItems);
                                                        }}
                                                        className="w-4 h-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                                                    />
                                                    <div className="flex-1">
                                                        <div className="flex items-center gap-2">
                                                            {item.type === 'rpc' ? <Code2 size={14} className="text-indigo-500" /> : <Zap size={14} className="text-amber-500" />}
                                                            <span className="text-sm font-bold text-slate-700">{item.name}</span>
                                                            <span className="text-[10px] px-2 py-0.5 rounded-full bg-slate-100 text-slate-500">{item.type}</span>
                                                            {item.existing && <span className="text-[10px] px-2 py-0.5 rounded-full bg-amber-100 text-amber-600">exists</span>}
                                                        </div>
                                                    </div>
                                                </div>
                                            ))}
                                        </div>
                                    )}
                                </div>
                                <div className="flex items-center justify-between">
                                    <div className="text-xs text-slate-500">
                                        {syncItems.filter(i => i.checked).length} selected ({syncItems.filter(i => i.checked && i.existing).length} will update)
                                    </div>
                                    <div className="flex gap-2">
                                        <button onClick={() => setShowSyncModal(false)} className="px-4 py-2 text-xs font-bold text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
                                        <button
                                            onClick={executeSync}
                                            disabled={syncItems.filter(i => i.checked).length === 0 || syncLoading}
                                            className="px-4 py-2 bg-emerald-600 text-white rounded-xl text-xs font-bold shadow-lg hover:bg-emerald-700 flex items-center gap-2 disabled:opacity-50"
                                        >
                                            {syncLoading ? <Loader2 size={14} className="animate-spin" /> : <GitCompare size={14} />}
                                            Import Selected
                                        </button>
                                    </div>
                                </div>
                            </>
                        )}
                    </div>
                </div>
            )}

            {/* DELETE MODAL */}
            {showDeleteModal && (
                <div className="fixed inset-0 bg-slate-950/50 backdrop-blur-sm z-[60] flex items-center justify-center p-4">
                    <div className="bg-white rounded-2xl p-6 w-full max-w-sm shadow-2xl">
                        <h3 className="font-bold text-slate-900 mb-2">Delete Asset</h3>
                        <p className="text-xs text-slate-500 mb-6">Are you sure you want to delete <b>{targetAsset?.name}</b>?</p>
                        <div className="flex gap-2">
                            <button onClick={() => setShowDeleteModal(false)} className="flex-1 py-3 text-xs font-bold text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
                            <button onClick={handleDeleteConfirm} className="flex-1 py-3 bg-rose-600 text-white rounded-xl text-xs font-bold shadow-lg hover:bg-rose-700">Delete</button>
                        </div>
                    </div>
                </div>
            )}

            {/* GLOBAL SECRETS MODAL — Password-protected reveal */}
            {showSecretsModal && (
                <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[100] flex items-center justify-center p-8 animate-in zoom-in-95">
                    <div className="bg-white rounded-[3.5rem] w-full max-w-2xl p-12 shadow-2xl border border-slate-100">
                        <header className="flex items-center justify-between mb-8">
                            <div className="flex items-center gap-4">
                                <div className="w-12 h-12 bg-amber-500 text-white rounded-2xl flex items-center justify-center shadow-lg"><Terminal size={20} /></div>
                                <div>
                                    <h3 className="text-2xl font-black text-slate-900 tracking-tighter">Global Variables</h3>
                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest">Environment Secrets for Edge Functions</p>
                                </div>
                            </div>
                            <button onClick={() => { setShowSecretsModal(false); setRevealedSecrets({}); }} className="p-2 hover:bg-slate-100 rounded-full text-slate-400 transition-colors"><X size={24} /></button>
                        </header>

                        <div className="space-y-6">
                            <div className="flex gap-4">
                                <input value={newSecretKey} onChange={(e) => setNewSecretKey(e.target.value.toUpperCase())} placeholder="API_KEY" className="flex-1 bg-slate-50 border border-slate-200 rounded-2xl py-3 px-6 text-sm font-bold outline-none focus:ring-4 focus:ring-amber-500/10 font-mono" />
                                <input value={newSecretVal} onChange={(e) => setNewSecretVal(e.target.value)} placeholder="Value..." type="password" className="flex-[2] bg-slate-50 border border-slate-200 rounded-2xl py-3 px-6 text-sm font-medium outline-none focus:ring-4 focus:ring-amber-500/10" />
                                <button onClick={addSecret} disabled={!newSecretKey} className="bg-amber-500 text-white px-4 rounded-2xl hover:bg-amber-600 transition-all shadow-lg"><Plus size={20} /></button>
                            </div>

                            <div className="space-y-3 max-h-[400px] overflow-y-auto pr-2 custom-scrollbar">
                                {globalSecrets.map((secret: any) => (
                                    <div key={secret.id} className="flex items-center justify-between bg-slate-50 p-4 rounded-2xl border border-slate-100 group hover:border-amber-200 transition-colors">
                                        <div className="flex items-center gap-3 overflow-hidden">
                                            <span className="text-[10px] font-black text-slate-700 font-mono bg-white px-2 py-1 rounded border border-slate-200">{secret.name}</span>
                                            <span className="text-xs font-mono text-slate-500 truncate max-w-[200px]">
                                                {revealedSecrets[secret.id] !== undefined ? revealedSecrets[secret.id] : '••••••••••••••••'}
                                            </span>
                                        </div>
                                        <div className="flex items-center gap-1">
                                            <button
                                                onClick={async () => {
                                                    if (revealedSecrets[secret.id] !== undefined) {
                                                        const next = { ...revealedSecrets }; delete next[secret.id]; setRevealedSecrets(next);
                                                    } else {
                                                        const pwd = prompt('Enter your admin password to reveal this secret:');
                                                        if (pwd) {
                                                            try {
                                                                const loginRes = await fetch('/api/control/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: pwd }) });
                                                                if (loginRes.ok) {
                                                                    const revealRes = await fetchWithAuth(`/api/control/projects/${projectId}/vault/${secret.id}/reveal`, { method: 'POST' });
                                                                    setRevealedSecrets(prev => ({ ...prev, [secret.id]: revealRes.value }));
                                                                } else {
                                                                    setError('Authentication failed.');
                                                                }
                                                            } catch (e) { setError('Failed to reveal'); }
                                                        }
                                                    }
                                                }}
                                                className="text-slate-300 hover:text-indigo-600 p-2" title={revealedSecrets[secret.id] !== undefined ? 'Hide' : 'Reveal (requires password)'}
                                            >
                                                {revealedSecrets[secret.id] !== undefined ? <EyeOff size={14} /> : <Eye size={14} />}
                                            </button>
                                            <button onClick={() => { copyToClipboard(revealedSecrets[secret.id] || '••••••'); }} className="text-slate-300 hover:text-indigo-600 p-2" title="Copy Value (Must reveal first)">
                                                <Copy size={14} />
                                            </button>
                                            <button onClick={() => { copyToClipboard(`env.${secret.name}`); }} className="text-slate-300 hover:text-amber-600 p-2" title="Copy Route (env.KEY)">
                                                <LinkIcon size={14} />
                                            </button>
                                            <button onClick={() => removeSecret(secret.id, secret.name)} className="text-slate-300 hover:text-rose-600 p-2" title="Delete"><X size={14} /></button>
                                        </div>
                                    </div>
                                ))}
                                {globalSecrets.length === 0 && <p className="text-center text-slate-400 text-xs py-10 font-bold uppercase tracking-widest">No variables defined</p>}
                            </div>
                        </div>
                    </div>
                </div>
            )}

            {/* Edge Function History Modal */}
            <EdgeHistoryModal
                isOpen={showHistoryModal}
                onClose={() => setShowHistoryModal(false)}
                projectId={projectId}
                functionName={selectedAsset?.name || ''}
                currentCode={editorSql}
                currentTimeoutMs={timeoutSec * 1000}
                currentEnvVars={envVars}
                currentImports={edgeImports}
                fetchWithAuth={fetchWithAuth}
                onRollbackSuccess={(newCode, timeoutMs, newEnvVars, newImports) => {
                    setEditorSql(newCode);
                    setOriginalSql(newCode);
                    setTimeoutSec(timeoutMs / 1000);
                    setEnvVars(newEnvVars);
                    setEdgeImports(newImports);
                    setIsDirty(false);
                    setSuccessMsg(`Successfully rolled back to version!`);
                    setTimeout(() => setSuccessMsg(null), 2500);
                    fetchData(); // reload
                }}
            />
        </div>
    );
};

// --- ICON COMPONENT FOR LOGS TAB ---
const ScrollText = ({ size, className }: any) => (
    <svg xmlns="http://www.w3.org/2000/svg" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}><path d="M8 21h12a2 2 0 0 0 2-2v-2H10v2a2 2 0 1 1-4 0V5a2 2 0 1 0-4 0v3h4" /><path d="M19 17V5a2 2 0 0 0-2-2H4" /></svg>
);

export default RPCManager;
