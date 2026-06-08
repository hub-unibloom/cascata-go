import React, { useState, useEffect } from 'react';
import {
  Zap,
  ShieldCheck,
  Table as TableIcon,
  Search,
  X,
  ChevronRight,
  Plus,
  Globe,
  Database,
  Variable,
  Webhook,
  Bot,
  BrainCircuit,
  ArrowRight,
  Mail,
  Code2,
  HardDrive,
  Split,
  Folder,
  Key,
  Terminal,
  FileText,
  ChevronLeft,
  User,
  Fingerprint,
  Clock,
  Hash,
  UserCheck,
  Wand2,
  Settings2,
  Scissors
} from 'lucide-react';

// Tipos para o componente
interface VariablePickerModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (variable: string) => void;
  availableNodes: any[];
  projectId: string;
  testLogs?: any[];
}

interface VariableMapperProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  availableNodes?: any[];
  expectedType?: string;
  placeholder?: string;
  projectId?: string;
  testLogs?: any[];
}

interface NodeDefinition {
  id: string;
  type: string;
  label: string;
  description: string;
  icon: any;
  color: string;
  category: string;
  inputs: string[];
  outputs: string[];
}

// Schema completo do WebhookEnvelope (sincronizado com backend/internal/controllers/webhook.go)
const WEBHOOK_ENVELOPE_SCHEMA = {
  receiver_id: 'string',
  project_slug: 'string',
  path_slug: 'string',
  branch_name: 'string',
  received_at: 'string',
  received_unix: 'number',
  method: 'string',
  url: 'string',
  path: 'string',
  raw_query: 'string',
  query_params: 'object',
  protocol: 'string',
  client_ip: 'string',
  user_agent: 'string',
  referer: 'string',
  forwarded_for: 'string',
  cf_connecting_ip: 'string',
  cf_country: 'string',
  cf_ray: 'string',
  cf_visitor: 'string',
  headers: 'object',
  content_type: 'string',
  body_format: 'string',
  body: 'object',
  raw_body: 'string',
  body_size: 'number',
  form_fields: 'object',
  file_names: 'array',
  auth_method: 'string',
  user_role: 'string',
  user_id: 'string',
  app_client_id: 'string',
  source_hints: 'object'
};

// NODE_LIBRARY sincronizada com NexusArchitect.tsx para manter identidade visual
const NODE_LIBRARY: NodeDefinition[] = [
  // TRIGGERS
  { id: 'webhook_trigger', type: 'trigger', label: 'Webhook', description: 'Inicia o fluxo via HTTP POST', icon: Webhook, color: 'bg-amber-500', category: 'Gatilhos', inputs: [], outputs: ['json'] },
  { id: 'pre_event_trigger', type: 'trigger', label: 'Pré-Evento (Sequestro)', description: 'Intercepta operações CRUD ANTES do banco', icon: ShieldCheck, color: 'bg-rose-600', category: 'Gatilhos', inputs: [], outputs: ['json'] },
  { id: 'post_event_trigger', type: 'trigger', label: 'Pós-Evento (Sequestro)', description: 'Intercepta operações CRUD APÓS o banco', icon: Zap, color: 'bg-indigo-600', category: 'Gatilhos', inputs: [], outputs: ['json'] },
  { id: 'cron_trigger', type: 'trigger', label: 'Schedule', description: 'Executa em intervalos fixos', icon: Zap, color: 'bg-amber-400', category: 'Gatilhos', inputs: [], outputs: ['any'] },
  
  // AI & AGENTS
  { id: 'ai_agent', type: 'ai', label: 'Nexus Agent', description: 'Agente autônomo com memória', icon: Bot, color: 'bg-indigo-600', category: 'IA & Agentes', inputs: ['packet'], outputs: ['packet'] },
  { id: 'llm_prompt', type: 'ai', label: 'LLM Prompt', description: 'Executa um prompt específico', icon: BrainCircuit, color: 'bg-indigo-500', category: 'IA & Agentes', inputs: ['string'], outputs: ['string'] },
  
  // ACTIONS
  { id: 'database_action', type: 'action', label: 'Database Action', description: 'Executa INSERT/UPDATE/DELETE no banco', icon: Database, color: 'bg-emerald-600', category: 'Ações', inputs: ['json'], outputs: ['json'] },
  { id: 'response_node', type: 'action', label: 'Response Payload', description: 'Define o formato de resposta ao cliente (JSON ou Texto)', icon: ArrowRight, color: 'bg-slate-900', category: 'Ações', inputs: ['any'], outputs: [] },
  { id: 'http_request', type: 'action', label: 'HTTP Request', description: 'Chamada API externa (REST)', icon: Globe, color: 'bg-blue-500', category: 'Ações', inputs: ['json'], outputs: ['json'] },
  { id: 'db_query', type: 'action', label: 'SQL Query', description: 'Executa comando no banco', icon: TableIcon, color: 'bg-emerald-500', category: 'Ações', inputs: ['json'], outputs: ['json'] },
  { id: 'email_send', type: 'action', label: 'Send Email', description: 'Envia notificação via SMTP', icon: Mail, color: 'bg-sky-500', category: 'Ações', inputs: ['json'], outputs: ['boolean'] },
  { id: 'js_code', type: 'action', label: 'JS Code', description: 'Executa script customizado', icon: Code2, color: 'bg-yellow-500', category: 'Ações', inputs: ['any'], outputs: ['any'] },
  
  // TOOLS
  { id: 'qdrant_search', type: 'tool', label: 'Vector Search', description: 'Busca semântica no Qdrant', icon: HardDrive, color: 'bg-rose-500', category: 'Ferramentas', inputs: ['string'], outputs: ['json'] },
  { id: 'security_gate', type: 'tool', label: 'Security Gate', description: 'Valida permissões Tier-1', icon: ShieldCheck, color: 'bg-red-600', category: 'Ferramentas', inputs: ['packet'], outputs: ['packet'] },
  
  // LOGIC
  { id: 'condition_if', type: 'condition', label: 'Logic Router (If/Else)', description: 'Roteamento lógico multi-canal e condicional', icon: Split, color: 'bg-purple-600', category: 'Lógica', inputs: ['any'], outputs: ['any', 'any'] }
];

// ============================================================================
// MODAL: VARIABLE PICKER
// ============================================================================

export const VariablePickerModal: React.FC<VariablePickerModalProps> = ({
  isOpen,
  onClose,
  onSelect,
  availableNodes,
  projectId,
  testLogs = []
}) => {
  const [search, setSearch] = useState('');
  const [activeTab, setActiveTab] = useState<'nodes' | 'vault' | 'enums' | 'user'>('nodes');
  const [expandedNodes, setExpandedNodes] = useState<string[]>([]);
  const [vaultItems, setVaultItems] = useState<any[]>([]);
  const [isLoadingVault, setIsLoadingVault] = useState(false);
  const [vaultPath, setVaultPath] = useState<Array<{ id: string; name: string }>>([]);
  const [enumItems, setEnumItems] = useState<any[]>([]);
  const [isLoadingEnums, setIsLoadingEnums] = useState(false);

  const flattenForPicker = (obj: any, prefix = ''): { path: string, type: string }[] => {
    if (!obj || typeof obj !== 'object') return [];
    
    const result: { path: string, type: string }[] = [];
    const visited = new Set<string>(); // Evita ciclos infinitos
    
    const recurse = (currentObj: any, currentPrefix: string) => {
      // Evita ciclos (ex: objetos que se referenciam)
      if (visited.has(currentPrefix)) return;
      visited.add(currentPrefix);
      
      Object.keys(currentObj).forEach(k => {
        const currentPath = currentPrefix ? `${currentPrefix}.${k}` : k;
        const value = currentObj[k];
        
        if (typeof value === 'object' && value !== null) {
          if (Array.isArray(value)) {
            // Para arrays, mostra o array e também tenta expandir o primeiro elemento se for objeto
            result.push({ path: currentPath, type: 'array' });
            if (value.length > 0 && typeof value[0] === 'object' && value[0] !== null) {
              // Expande o primeiro elemento do array como exemplo (ex: body.entry[0].*)
              recurse(value[0], `${currentPath}[0]`);
            }
          } else {
            // Para objetos, mostra o objeto e expande recursivamente SEM LIMITE
            result.push({ path: currentPath, type: 'object' });
            recurse(value, currentPath);
          }
        } else {
          result.push({ path: currentPath, type: typeof value });
        }
      });
    };
    
    recurse(obj, prefix);
    return result;
  };

  useEffect(() => {
    if (isOpen && activeTab === 'vault' && projectId) {
      setVaultPath([]);
      fetchVaultItems(null);
    }
    if (isOpen && activeTab === 'enums' && projectId) {
      fetchEnumItems();
    }
    if (isOpen && activeTab === 'user' && projectId) {
      fetchUserSchema();
    }
  }, [isOpen, activeTab, projectId]);

  const fetchVaultItems = async (parentId: string | null = null) => {
    setIsLoadingVault(true);
    try {
      const res = await fetch(`/api/control/projects/${projectId}/vault?parentId=${parentId || 'root'}`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      const data = await res.json();
      if (res.ok) {
        setVaultItems(Array.isArray(data) ? data : []);
      }
    } catch (e) {
      console.error("Failed to fetch vault items", e);
    } finally {
      setIsLoadingVault(false);
    }
  };

  const fetchEnumItems = async () => {
    if (!projectId) return;
    setIsLoadingEnums(true);
    try {
      const res = await fetch(`/api/data/${projectId}/enum-types`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      const data = await res.json();
      if (res.ok) {
        setEnumItems(Array.isArray(data) ? data : []);
      }
    } catch (e) {
      console.error("Failed to fetch enum items", e);
    } finally {
      setIsLoadingEnums(false);
    }
  };

  // Busca tabelas do schema auth + tabelas concatenadas do public
  const fetchUserSchema = async () => {
    setIsLoadingUser(true);
    try {
      const token = localStorage.getItem('cascata_token');

      // 1. Busca tabelas do schema auth
      const authRes = await fetch(`/api/data/${projectId}/tables?schema=auth`, {
        headers: { Authorization: `Bearer ${token}` }
      });
      const authData = authRes.ok ? await authRes.json() : [];
      const authTables = (Array.isArray(authData) ? authData : []).map((t: any) => ({
        name: t.name,
        schema: 'auth',
        isConcat: false
      }));

      // 2. Busca tabelas concatenadas (linked_tables do metadata do projeto)
      let concatTables: Array<{ name: string; schema: string; isConcat: boolean }> = [];
      try {
        const projRes = await fetch('/api/control/projects', {
          headers: { Authorization: `Bearer ${token}` }
        });
        if (projRes.ok) {
          const projects = await projRes.json();
          const current = projects.find((p: any) => p.slug === projectId);
          const linked = current?.metadata?.extra?.linked_tables || [];
          concatTables = linked.map((name: string) => ({
            name,
            schema: 'public',
            isConcat: true
          }));
        }
      } catch (e) {
        console.error('Failed to fetch linked tables', e);
      }

      setUserTables([...authTables, ...concatTables]);
    } catch (e) {
      console.error('Failed to fetch user schema', e);
    } finally {
      setIsLoadingUser(false);
    }
  };

  // Busca colunas de uma tabela específica (lazy: só quando expandida)
  const fetchTableColumns = async (tableName: string, schema: string) => {
    if (userTableColumns[`${schema}.${tableName}`]) return; // Já carregou
    setLoadingColumns(`${schema}.${tableName}`);
    try {
      const res = await fetch(`/api/data/${projectId}/tables/${tableName}/columns?schema=${schema}`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      if (res.ok) {
        const cols = await res.json();
        setUserTableColumns(prev => ({
          ...prev,
          [`${schema}.${tableName}`]: Array.isArray(cols) ? cols : []
        }));
      }
    } catch (e) {
      console.error(`Failed to fetch columns for ${schema}.${tableName}`, e);
    } finally {
      setLoadingColumns(null);
    }
  };

  const toggleUserTable = (key: string, tableName: string, schema: string) => {
    const isExpanding = !expandedUserTables.includes(key);
    setExpandedUserTables(prev =>
      prev.includes(key) ? prev.filter(k => k !== key) : [...prev, key]
    );
    if (isExpanding) {
      fetchTableColumns(tableName, schema);
    }
  };

  const getVaultItemIcon = (type: string) => {
    switch (type) {
      case 'folder': return Folder;
      case 'key': return Key;
      case 'cert': return ShieldCheck;
      case 'env': return Terminal;
      case 'file': return FileText;
      default: return Key;
    }
  };

  if (!isOpen) return null;

  const toggleNode = (id: string) => {
    setExpandedNodes(prev => prev.includes(id) ? prev.filter(i => i !== id) : [...prev, id]);
  };

  const filteredNodes = availableNodes.filter(n =>
    n.label.toLowerCase().includes(search.toLowerCase()) ||
    Object.keys(n.schema || {}).some(k => k.toLowerCase().includes(search.toLowerCase()))
  );

  const filteredVault = vaultItems.filter(v => 
    v.name.toLowerCase().includes(search.toLowerCase())
  );

  const filteredEnums = enumItems.filter(e => 
    e.name.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div className="fixed inset-0 z-[200] flex items-center justify-center p-6 backdrop-blur-md bg-slate-900/40 animate-in fade-in duration-300">
      <div className="bg-white w-full max-w-md rounded-[2.5rem] shadow-[0_40px_80px_-15px_rgba(0,0,0,0.3)] border border-slate-100 overflow-hidden flex flex-col max-h-[600px] animate-in zoom-in-95">
        <div className="px-8 py-6 border-b border-slate-50 flex items-center justify-between bg-slate-50/50">
          <div>
            <h3 className="text-lg font-black text-slate-900 uppercase tracking-tighter">Variáveis Disponíveis Modal</h3>
            <p className="text-[9px] text-slate-400 font-black uppercase tracking-widest mt-1">
              {activeTab === 'nodes' && "Dados dos nós precedentes"}
              {activeTab === 'vault' && "Segredos protegidos do projeto"}
              {activeTab === 'enums' && "Listas e Enums globais"}
              {activeTab === 'user' && "Dados do usuário autenticado"}
            </p>
          </div>
          <button onClick={onClose} className="w-10 h-10 hover:bg-slate-200 rounded-xl flex items-center justify-center text-slate-400">
            <X size={20} />
          </button>
        </div>

        {/* TABS */}
        <div className="flex px-6 pt-2 bg-slate-50/30 border-b border-slate-50">
          {[
            { id: 'nodes', label: 'Nós', icon: Zap },
            { id: 'vault', label: 'Vault', icon: ShieldCheck },
            { id: 'enums', label: "Enum's", icon: TableIcon },
            { id: 'user', label: 'User', icon: User }
          ].map(tab => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id as any)}
              className={`flex-1 flex items-center justify-center gap-2 py-4 border-b-2 transition-all ${
                activeTab === tab.id 
                  ? 'border-indigo-500 text-indigo-600 bg-white' 
                  : 'border-transparent text-slate-400 hover:text-slate-600'
              }`}
            >
              <tab.icon size={14} className={activeTab === tab.id ? 'text-indigo-500' : 'text-slate-300'} />
              <span className="text-[10px] font-black uppercase tracking-widest">{tab.label}</span>
            </button>
          ))}
        </div>

        <div className="px-6 py-4">
          <div className="relative">
            <Search className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-300" size={16} />
            <input
              autoFocus
              type="text"
              placeholder={activeTab === 'vault' ? "Buscar segredo..." : "Buscar variável ou nó..."}
              className="w-full bg-slate-100 border-none rounded-2xl py-3 pl-12 pr-4 text-xs font-bold text-slate-800 outline-none focus:ring-2 focus:ring-indigo-500/20"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </div>

        <div className="flex-1 overflow-y-auto px-6 pb-8 space-y-3 custom-scrollbar min-h-[300px]">
          {activeTab === 'nodes' && (
            filteredNodes.length === 0 ? (
              <div className="py-10 text-center">
                <p className="text-[10px] font-black text-slate-300 uppercase tracking-widest italic">Nenhuma variável encontrada</p>
              </div>
            ) : (
              filteredNodes.map(n => {
                const isExpanded = expandedNodes.includes(n.id);
                const nodeDef = NODE_LIBRARY.find(d => d.id === n.nodeId) || NODE_LIBRARY[0];
                return (
                  <div key={n.id} className="border border-slate-100 rounded-2xl overflow-hidden shadow-sm">
                    <button
                      onClick={() => toggleNode(n.id)}
                      className="w-full px-5 py-4 flex items-center justify-between bg-white hover:bg-slate-50 transition-colors"
                    >
                      <div className="flex items-center gap-3">
                        <div className={`p-1.5 rounded-lg ${nodeDef.color} text-white`}>
                          <nodeDef.icon size={14} />
                        </div>
                        <span className="text-[10px] font-black text-slate-700 uppercase">{n.label}</span>
                      </div>
                      <ChevronRight size={14} className={`text-slate-300 transition-transform ${isExpanded ? 'rotate-90' : ''}`} />
                    </button>

                    {isExpanded && (
                      <div className="bg-slate-50/50 p-2 space-y-1 border-t border-slate-50">
                        {(() => {
                          const nodeLog = testLogs.find(l => l.node_id === n.id);
                          const realData = nodeLog?.output_data || nodeLog?.input_data;
                          
                          let variables: { path: string, type: string }[] = [];
                          
                          if (realData && typeof realData === 'object') {
                            variables = flattenForPicker(realData);
                          } else {
                            // Para webhook trigger, usa o schema completo do WebhookEnvelope
                            // Para outros nós, usa o schema estático do nó
                            const isWebhookTrigger = n.nodeId === 'webhook_trigger' || n.type === 'trigger';
                            const staticSchema = isWebhookTrigger ? WEBHOOK_ENVELOPE_SCHEMA : (n.schema || { payload: 'object', status: 'string' });
                            variables = Object.entries(staticSchema).map(([prop, type]) => ({ path: prop, type: type as string }));
                          }

                          const filteredVars = variables.filter(v => {
                            const pathStr = String(v.path || '');
                            return pathStr.toLowerCase().includes(search.toLowerCase());
                          });

                          if (filteredVars.length === 0) {
                            return <p className="p-4 text-[9px] text-slate-400 italic text-center uppercase tracking-widest font-black">Aguardando execução para mapear campos reais</p>;
                          }

                          return filteredVars.map(({ path, type }) => (
                            <button
                              key={path}
                              onClick={() => {
                                const isTrigger = n.type === 'trigger';
                                const variable = isTrigger
                                  ? (availableNodes.filter(node => node.type === 'trigger').length > 1 
                                      ? `$nodes.${n.id}.${path}` 
                                      : `$trigger.${path}`)
                                  : `$nodes.${n.id}.${path}`;
                                onSelect(variable);
                              }}
                              className="w-full p-3 rounded-xl hover:bg-white hover:shadow-sm text-left flex items-center justify-between group transition-all animate-in slide-in-from-left-2"
                            >
                              <div className="flex flex-col">
                                <span className="text-[10px] font-mono text-indigo-600 font-bold">{path}</span>
                                {path.includes('.') && <span className="text-[7px] text-slate-400 uppercase font-black tracking-tighter">Nested Field</span>}
                              </div>
                              <span className="text-[8px] font-black uppercase text-slate-300 group-hover:text-indigo-400 transition-colors">{type}</span>
                            </button>
                          ));
                        })()}
                      </div>
                    )}
                  </div>
                );
              })
            )
          )}

          {activeTab === 'vault' && (
            <div className="space-y-3">
              {/* Breadcrumb / Navegação */}
              {vaultPath.length > 0 && (
                <button
                  onClick={() => {
                    const newPath = vaultPath.slice(0, -1);
                    setVaultPath(newPath);
                    const parentId = newPath.length > 0 ? newPath[newPath.length - 1].id : null;
                    fetchVaultItems(parentId);
                  }}
                  className="flex items-center gap-2 px-3 py-2 text-[10px] font-bold text-slate-500 hover:text-indigo-600 uppercase tracking-widest transition-colors"
                >
                  <ChevronLeft size={14} />
                  Voltar
                </button>
              )}

              {isLoadingVault ? (
                <div className="py-10 text-center">
                  <p className="text-[10px] font-black text-slate-300 uppercase tracking-widest animate-pulse">Carregando cofre...</p>
                </div>
              ) : filteredVault.length === 0 ? (
                <div className="py-10 text-center">
                  <p className="text-[10px] font-black text-slate-300 uppercase tracking-widest italic">
                    {vaultPath.length > 0 ? 'Pasta vazia' : 'Nenhum segredo disponível'}
                  </p>
                </div>
              ) : (
                <div className="grid grid-cols-1 gap-2">
                  {filteredVault.map(item => {
                    const ItemIcon = getVaultItemIcon(item.type);
                    const isFolder = item.type === 'folder';
                    const vaultPrefix = vaultPath.map((p: { name: string }) => p.name).join('.');
                    const variableName = vaultPrefix ? `$vault.${vaultPrefix}.${item.name}.value` : `$vault.${item.name}.value`;

                    return (
                      <button
                        key={item.id}
                        onClick={() => {
                          if (isFolder) {
                            setVaultPath([...vaultPath, { id: item.id, name: item.name }]);
                            fetchVaultItems(item.id);
                          } else {
                            onSelect(variableName);
                          }
                        }}
                        className={`w-full p-4 rounded-2xl border transition-all flex items-center justify-between group ${
                          isFolder
                            ? 'border-slate-100 hover:border-amber-200 hover:bg-amber-50/30 bg-slate-50/50'
                            : 'border-slate-100 hover:border-indigo-200 hover:bg-indigo-50/30'
                        }`}
                      >
                        <div className="flex items-center gap-3">
                          <div className={`w-8 h-8 rounded-lg flex items-center justify-center transition-colors ${
                            isFolder
                              ? 'bg-amber-100 text-amber-500 group-hover:bg-amber-500 group-hover:text-white'
                              : 'bg-slate-100 text-slate-400 group-hover:bg-indigo-500 group-hover:text-white'
                          }`}>
                            <ItemIcon size={14} />
                          </div>
                          <div className="flex flex-col">
                            <span className="text-[10px] font-black text-slate-700 uppercase">{item.name}</span>
                            <span className="text-[8px] font-bold text-slate-400 uppercase tracking-tighter">{item.type}</span>
                          </div>
                        </div>
                        {isFolder ? (
                          <ChevronRight size={14} className="text-slate-300 group-hover:text-amber-400" />
                        ) : (
                          <Plus size={14} className="text-slate-200 group-hover:text-indigo-400" />
                        )}
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          )}

          {activeTab === 'enums' && (
            <div className="space-y-3">
              {isLoadingEnums ? (
                <div className="py-10 text-center">
                  <p className="text-[10px] font-black text-slate-300 uppercase tracking-widest animate-pulse">Carregando enums...</p>
                </div>
              ) : filteredEnums.length === 0 ? (
                <div className="py-10 text-center">
                  <div className="w-16 h-16 bg-slate-50 rounded-full flex items-center justify-center mx-auto mb-4 border border-slate-100">
                    <TableIcon size={24} className="text-slate-200" />
                  </div>
                  <p className="text-[10px] font-black text-slate-300 uppercase tracking-widest italic">Nenhum Enum configurado</p>
                  <p className="text-[8px] text-slate-400 mt-2 px-10">Os Enums globais do projeto aparecerão aqui quando configurados.</p>
                </div>
              ) : (
                <div className="grid grid-cols-1 gap-3">
                  {filteredEnums.map((enumItem: any, enumIndex: number) => (
                    <div key={`${enumItem.name}-${enumIndex}`} className="border border-slate-100 rounded-2xl overflow-hidden shadow-sm">
                      {/* Header do Enum - Seleção da enum inteira */}
                      <button
                        onClick={() => {
                          const variableName = `$enums.${enumItem.name}`;
                          onSelect(variableName);
                        }}
                        className="w-full px-5 py-4 flex items-center justify-between bg-gradient-to-r from-indigo-50 to-purple-50 hover:from-indigo-100 hover:to-purple-100 transition-colors border-b border-indigo-100"
                      >
                        <div className="flex items-center gap-3">
                          <div className="p-1.5 rounded-lg bg-gradient-to-r from-indigo-500 to-purple-500 text-white">
                            <TableIcon size={14} />
                          </div>
                          <div className="text-left">
                            <span className="text-[10px] font-black text-indigo-700 uppercase">{enumItem.name}</span>
                            <div className="flex items-center gap-2 mt-1">
                              <span className="text-[8px] font-bold text-indigo-500 uppercase tracking-tighter bg-white px-2 py-0.5 rounded">
                                {enumItem.schema}
                              </span>
                              <span className="text-[8px] font-bold text-slate-400">
                                {enumItem.values?.length || 0} valores
                              </span>
                            </div>
                          </div>
                        </div>
                        <div className="flex items-center gap-2">
                          <span className="text-[8px] font-black text-indigo-600 uppercase tracking-widest bg-white px-2 py-1 rounded border border-indigo-200">
                            Enum Completa
                          </span>
                          <ChevronRight size={14} className="text-indigo-400" />
                        </div>
                      </button>

                      {/* Valores individuais do Enum */}
                      {enumItem.values && enumItem.values.length > 0 && (
                        <div className="bg-slate-50/30 p-2 space-y-1 border-t border-slate-50">
                          <div className="px-2 py-1">
                            <span className="text-[8px] font-black text-slate-400 uppercase tracking-wider">Valores Individuais:</span>
                          </div>
                          {enumItem.values.map((value: string, valueIndex: number) => (
                            <button
                              key={`${enumItem.name}-${value}-${valueIndex}`}
                              onClick={() => {
                                const variableName = `$enums.${enumItem.name}.${value}`;
                                onSelect(variableName);
                              }}
                              className="w-full p-3 rounded-xl hover:bg-white hover:shadow-sm text-left flex items-center justify-between group transition-all border border-transparent hover:border-indigo-100"
                            >
                              <span className="text-[10px] font-mono text-indigo-600 font-bold">{value}</span>
                              <div className="flex items-center gap-2">
                                <span className="text-[8px] font-black text-slate-300 group-hover:text-indigo-400 transition-colors">
                                  valor
                                </span>
                                <Plus size={12} className="text-slate-200 group-hover:text-indigo-400 transition-colors" />
                              </div>
                            </button>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {activeTab === 'user' && (
            <div className="space-y-3">
              {/* Aviso de contexto */}
              <div className="p-4 bg-gradient-to-r from-amber-50 to-orange-50 rounded-2xl border border-amber-100">
                <div className="flex items-center gap-2 mb-2">
                  <UserCheck size={14} className="text-amber-600" />
                  <span className="text-[9px] font-black text-amber-700 uppercase tracking-widest">Contexto Autenticado</span>
                </div>
                <p className="text-[8px] text-amber-600 leading-relaxed">
                  Variáveis vinculadas ao usuário da requisição via <code className="bg-white/80 px-1 py-0.5 rounded text-[7px]">user_id</code>. Requerem autenticação ativa (Bearer Token / JWT).
                </p>
              </div>

              {isLoadingUser ? (
                <div className="py-10 text-center">
                  <p className="text-[10px] font-black text-slate-300 uppercase tracking-widest animate-pulse">Carregando schema do usuário...</p>
                </div>
              ) : userTables.length === 0 ? (
                <div className="py-10 text-center">
                  <div className="w-16 h-16 bg-slate-50 rounded-full flex items-center justify-center mx-auto mb-4 border border-slate-100">
                    <User size={24} className="text-slate-200" />
                  </div>
                  <p className="text-[10px] font-black text-slate-300 uppercase tracking-widest italic">Nenhuma tabela de usuário encontrada</p>
                </div>
              ) : (
                <div className="space-y-2">
                  {userTables
                    .filter(t =>
                      t.name.toLowerCase().includes(search.toLowerCase())
                    )
                    .map(table => {
                      const tableKey = `${table.schema}.${table.name}`;
                      const isExpanded = expandedUserTables.includes(tableKey);
                      const columns = userTableColumns[tableKey] || [];
                      const isLoadingCols = loadingColumns === tableKey;
                      const isUsersTable = table.schema === 'auth' && table.name === 'users';
                      const linkField = isUsersTable ? 'id' : 'user_id';

                      return (
                        <div key={tableKey} className="border border-slate-100 rounded-2xl overflow-hidden shadow-sm">
                          {/* Table Header */}
                          <button
                            onClick={() => toggleUserTable(tableKey, table.name, table.schema)}
                            className={`w-full px-5 py-4 flex items-center justify-between transition-colors ${
                              table.isConcat
                                ? 'bg-gradient-to-r from-emerald-50 to-teal-50 hover:from-emerald-100 hover:to-teal-100'
                                : 'bg-gradient-to-r from-indigo-50 to-purple-50 hover:from-indigo-100 hover:to-purple-100'
                            }`}
                          >
                            <div className="flex items-center gap-3">
                              <div className={`p-1.5 rounded-lg text-white ${
                                table.isConcat
                                  ? 'bg-gradient-to-r from-emerald-500 to-teal-500'
                                  : 'bg-gradient-to-r from-indigo-500 to-purple-500'
                              }`}>
                                {table.isConcat ? <Database size={14} /> : <Fingerprint size={14} />}
                              </div>
                              <div className="text-left">
                                <span className="text-[10px] font-black text-slate-700 uppercase">{table.name}</span>
                                <div className="flex items-center gap-2 mt-0.5">
                                  <span className={`text-[8px] font-bold uppercase tracking-tighter px-2 py-0.5 rounded ${
                                    table.isConcat
                                      ? 'bg-emerald-100 text-emerald-600'
                                      : 'bg-indigo-100 text-indigo-500'
                                  }`}>
                                    {table.schema}
                                  </span>
                                  <span className="text-[7px] font-bold text-slate-400">
                                    via {linkField}
                                  </span>
                                  {table.isConcat && (
                                    <span className="text-[7px] font-black text-emerald-500 uppercase tracking-wider">concatenated</span>
                                  )}
                                </div>
                              </div>
                            </div>
                            <ChevronRight size={14} className={`text-slate-300 transition-transform ${isExpanded ? 'rotate-90' : ''}`} />
                          </button>

                          {/* Columns */}
                          {isExpanded && (
                            <div className="bg-slate-50/30 p-2 space-y-1 border-t border-slate-50">
                              {isLoadingCols ? (
                                <div className="py-4 text-center">
                                  <p className="text-[9px] font-bold text-slate-300 animate-pulse">Carregando colunas...</p>
                                </div>
                              ) : columns.length === 0 ? (
                                <div className="py-4 text-center">
                                  <p className="text-[9px] font-bold text-slate-300 italic">Sem colunas</p>
                                </div>
                              ) : (
                                columns
                                  .filter((col: any) =>
                                    !search || col.name.toLowerCase().includes(search.toLowerCase())
                                  )
                                  .map((col: any) => (
                                    <button
                                      key={col.name}
                                      onClick={() => {
                                        onSelect(`$user.${table.name}.${col.name}`);
                                      }}
                                      className="w-full p-3 rounded-xl hover:bg-white hover:shadow-sm text-left flex items-center justify-between group transition-all border border-transparent hover:border-indigo-100"
                                    >
                                      <div className="flex flex-col">
                                        <span className="text-[10px] font-mono text-indigo-600 font-bold">$user.{table.name}.{col.name}</span>
                                        {col.isPrimaryKey && (
                                          <span className="text-[7px] font-black text-amber-500 uppercase">Primary Key</span>
                                        )}
                                      </div>
                                      <span className="text-[8px] font-black uppercase text-slate-300 group-hover:text-indigo-400 transition-colors">
                                        {col.type?.replace('character varying', 'varchar').replace('timestamp with time zone', 'timestamptz').replace('without time zone', '') || 'unknown'}
                                      </span>
                                    </button>
                                  ))
                              )}
                            </div>
                          )}
                        </div>
                      );
                    })}
                </div>
              )}

              {/* Dica dinâmica */}
              <div className="p-4 bg-slate-50 rounded-2xl border border-slate-100">
                <p className="text-[8px] text-slate-500 leading-relaxed">
                  <strong className="text-slate-600">Dinâmica:</strong> Tabelas do schema <code className="bg-white px-1 py-0.5 rounded text-indigo-500 text-[7px]">auth</code> são nativas do sistema de identidade. Tabelas com tag <code className="bg-white px-1 py-0.5 rounded text-emerald-500 text-[7px]">concatenated</code> foram linkadas via Schema Concatenation e usam <code className="bg-white px-1 py-0.5 rounded text-indigo-500 text-[7px]">user_id</code> como FK. Todas são filtradas automaticamente pelo usuário da requisição.
                </p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

// ============================================================================
// MODAL: PIPE PICKER (Transformations)
// ============================================================================

interface PipePickerModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (pipe: string) => void;
}

export const PipePickerModal: React.FC<PipePickerModalProps> = ({ isOpen, onClose, onSelect }) => {
  if (!isOpen) return null;

  const PIPES = [
    { id: 'to_number', label: 'To Number', desc: 'Converte string em número decimal', icon: Hash, color: 'text-amber-500', bg: 'bg-amber-50' },
    { id: 'to_int', label: 'To Integer', desc: 'Converte em número inteiro', icon: Hash, color: 'text-orange-500', bg: 'bg-orange-50' },
    { id: 'to_string', label: 'To String', desc: 'Converte qualquer dado em texto', icon: FileText, color: 'text-blue-500', bg: 'bg-blue-50' },
    { id: 'to_bool', label: 'To Boolean', desc: 'Converte em verdadeiro/falso', icon: ShieldCheck, color: 'text-emerald-500', bg: 'bg-emerald-50' },
    { id: 'to_timestamp', label: 'To Timestamp', desc: 'Converte em data/hora ISO', icon: Clock, color: 'text-violet-500', bg: 'bg-violet-50' },
    { id: 'uppercase', label: 'Uppercase', desc: 'Tudo em MAIÚSCULO', icon: ArrowRight, color: 'text-indigo-500', bg: 'bg-indigo-50' },
    { id: 'lowercase', label: 'Lowercase', desc: 'tudo em minúsculo', icon: ArrowRight, color: 'text-slate-500', bg: 'bg-slate-50' },
    { id: 'trim', label: 'Trim Space', desc: 'Remove espaços nas pontas', icon: Scissors, color: 'text-rose-500', bg: 'bg-rose-50' },
    { id: 'json', label: 'To JSON', desc: 'Serializa objeto em string JSON', icon: Code2, color: 'text-slate-900', bg: 'bg-slate-100' },
  ];

  return (
    <div className="fixed inset-0 z-[210] flex items-center justify-center p-6 backdrop-blur-md bg-slate-900/40 animate-in fade-in duration-300">
      <div className="bg-white w-full max-w-sm rounded-[2.5rem] shadow-[0_40px_80px_-15px_rgba(0,0,0,0.3)] border border-slate-100 overflow-hidden animate-in zoom-in-95">
        <div className="px-8 py-6 border-b border-slate-50 flex items-center justify-between bg-emerald-50/30">
          <div>
            <h3 className="text-lg font-black text-slate-900 uppercase tracking-tighter flex items-center gap-2">
              <Wand2 size={18} className="text-emerald-500" /> Transformar Dado
            </h3>
            <p className="text-[9px] text-slate-400 font-black uppercase tracking-widest mt-1">Aplique filtros e casting inline</p>
          </div>
          <button onClick={onClose} className="w-10 h-10 hover:bg-slate-200 rounded-xl flex items-center justify-center text-slate-400">
            <X size={20} />
          </button>
        </div>

        <div className="p-4 grid grid-cols-1 gap-2 max-h-[400px] overflow-y-auto custom-scrollbar">
          {PIPES.map(pipe => (
            <button
              key={pipe.id}
              onClick={() => {
                onSelect(pipe.id);
                onClose();
              }}
              className="group flex items-center gap-4 p-4 rounded-2xl hover:bg-slate-50 transition-all border border-transparent hover:border-slate-100 text-left"
            >
              <div className={`w-10 h-10 rounded-xl ${pipe.bg} ${pipe.color} flex items-center justify-center group-hover:scale-110 transition-transform`}>
                <pipe.icon size={18} />
              </div>
              <div>
                <span className="text-[10px] font-black text-slate-700 uppercase block">{pipe.label}</span>
                <span className="text-[8px] font-bold text-slate-400 uppercase tracking-tighter">{pipe.desc}</span>
              </div>
              <Plus size={14} className="ml-auto text-slate-200 group-hover:text-emerald-500" />
            </button>
          ))}
        </div>
      </div>
    </div>
  );
};

// ============================================================================
// COMPONENT: VARIABLE MAPPER
// ============================================================================

export const VariableMapper: React.FC<VariableMapperProps> = ({
  label,
  value,
  onChange,
  availableNodes = [],
  projectId,
  testLogs = []
}: VariableMapperProps) => {
  const [showVariablePicker, setShowVariablePicker] = useState(false);
  const [showPipePicker, setShowPipePicker] = useState(false);

  const applyPipe = (pipe: string) => {
    // Se o valor termina com }}, insere o pipe antes do fechamento
    if (value.trim().endsWith('}}')) {
      const newValue = value.trim().replace(/\}\}$ /g, '').replace(/\}\}$/g, ` | ${pipe}}}`);
      onChange(newValue);
    } else {
      // Caso contrário, apenas avisa ou não faz nada (idealmente o botão só aparece/funciona se houver var)
    }
  };

  return (
    <div className="space-y-2">
      {label && (
        <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.3em] flex items-center gap-2">
          <Variable size={12} />
          {label}
        </label>
      )}
      <div className="relative group/mapper">
        <input
          type="text"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 pr-20 text-sm font-medium text-slate-800 placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-300 transition-all"
        />
        <div className="absolute right-1.5 top-1/2 -translate-y-1/2 flex items-center gap-1">
          {typeof value === 'string' && value.includes('}}') && (
            <button
              type="button"
              onClick={() => setShowPipePicker(true)}
              className="p-2 bg-emerald-50 text-emerald-500 hover:bg-emerald-500 hover:text-white rounded-lg transition-all"
              title="Transformar dado (Cast)"
            >
              <Wand2 size={14} />
            </button>
          )}
          <button
            type="button"
            onClick={() => setShowVariablePicker(true)}
            className="p-2 bg-indigo-500 hover:bg-indigo-600 text-white rounded-lg transition-colors shadow-sm"
            title="Inserir Variável"
          >
            <Variable size={14} />
          </button>
        </div>
      </div>
      
      {showVariablePicker && (
        <VariablePickerModal
          isOpen={showVariablePicker}
          onClose={() => setShowVariablePicker(false)}
          projectId={projectId}
          testLogs={testLogs}
          onSelect={(variable) => {
            onChange(value + `{{${variable}}}`);
            setShowVariablePicker(false);
          }}
          availableNodes={availableNodes}
        />
      )}

      {showPipePicker && (
        <PipePickerModal
          isOpen={showPipePicker}
          onClose={() => setShowPipePicker(false)}
          onSelect={applyPipe}
        />
      )}
    </div>
  );
};