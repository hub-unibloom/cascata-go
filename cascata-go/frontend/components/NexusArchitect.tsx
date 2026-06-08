import React, { useState, useCallback, useMemo, useEffect } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  addEdge,
  Connection,
  Edge,
  Node,
  Handle,
  Position,
  Panel,
  ReactFlowProvider,
  getBezierPath,
  EdgeProps,
  BaseEdge,
  EdgeLabelRenderer,
  getSimpleBezierPath,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import {
  Zap,
  Database,
  Globe,
  MessageSquare,
  Plus,
  Save,
  Search,
  X,
  ChevronRight,
  Settings2,
  Trash2,
  Play,
  Cpu,
  Mail,
  Webhook,
  Code2,
  Table as TableIcon,
  HardDrive,
  ShieldCheck,
  ShieldAlert,
  Bot,
  BrainCircuit,
  ArrowRightLeft,
  ArrowRight,
  GitBranch,
  Split,
  Eye,
  Key,
  Copy,
  Check,
  Braces,
  Loader2,
  UserCheck,
  Lock,
  Radio,
  Wand2,
  ChevronDown,
  ChevronUp
} from 'lucide-react';
import { VariablePickerModal, VariableMapper, PipePickerModal } from './VariableMapper';
import { HTTPNodeSimple } from './HTTPNodeSimple';

// ============================================================================
// UTILS
// ============================================================================

const generateId = (prefix: string = 'node') => {
  const random = Math.random().toString(36).substring(2, 8);
  return `${prefix}_${Date.now()}_${random}`;
};

// ============================================================================
// TYPES & CONSTANTS
// ============================================================================

export type NodeType = 'trigger' | 'action' | 'condition' | 'ai' | 'tool';
export type SemanticType = 'any' | 'string' | 'number' | 'boolean' | 'json' | 'packet';

export interface NodeDefinition {
  id: string;
  type: NodeType;
  label: string;
  description: string;
  icon: any;
  color: string;
  category: string;
  inputs: SemanticType[];
  outputs: SemanticType[];
}

const TYPE_COLORS: Record<SemanticType, string> = {
  any: 'bg-slate-200 border-slate-400',
  string: 'bg-blue-100 border-blue-500',
  number: 'bg-cyan-100 border-cyan-500',
  boolean: 'bg-emerald-100 border-emerald-500',
  json: 'bg-amber-100 border-amber-500',
  packet: 'bg-indigo-100 border-indigo-500'
};

const NODE_LIBRARY: NodeDefinition[] = [
  // TRIGGERS
  { id: 'webhook_trigger', type: 'trigger', label: 'Webhook', description: 'Inicia o fluxo via HTTP POST', icon: Webhook, color: 'bg-amber-500', category: 'Gatilhos', inputs: [], outputs: ['json'] },
  { id: 'pre_event_trigger', type: 'trigger', label: 'Pré-Evento (Sequestro)', description: 'Intercepta operações CRUD ANTES do banco', icon: ShieldCheck, color: 'bg-rose-600', category: 'Gatilhos', inputs: [], outputs: ['json'] },
  { id: 'post_event_trigger', type: 'trigger', label: 'Pós-Evento (Sequestro)', description: 'Intercepta operações CRUD APÓS o banco', icon: Zap, color: 'bg-indigo-600', category: 'Gatilhos', inputs: [], outputs: ['json'] },
  { id: 'step_up_challenge_trigger', type: 'trigger', label: 'Step-Up Challenge Solicitado', description: 'Dispara quando um OTP/MFA customizado é solicitado', icon: ShieldAlert, color: 'bg-rose-700', category: 'Gatilhos', inputs: [], outputs: ['json'] },
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
  { id: 'condition_if', type: 'condition', label: 'Logic Router (If/Else)', description: 'Roteamento lógico multi-canal e condicional', icon: Split, color: 'bg-purple-600', category: 'Lógica', inputs: ['any'], outputs: ['any', 'any'] },
];

// ============================================================================
// CUSTOM EDGE WITH LABEL & "+" BUTTON
// ============================================================================

const PremiumEdge = ({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  style = {},
  markerEnd,
  label,
  data,
}: EdgeProps) => {
  const [edgePath, labelX, labelY] = getSimpleBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  const [isHovered, setIsHovered] = useState(false);

  const onAddClick = (evt: React.MouseEvent) => {
    evt.stopPropagation();
    if (data?.onAddNode) {
      data.onAddNode(id, { x: labelX, y: labelY });
    }
  };

  const onDeleteClick = (evt: React.MouseEvent) => {
    evt.stopPropagation();
    if (data?.onDeleteEdge) {
      data.onDeleteEdge(id);
    }
  };

  return (
    <g
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
      className="group/edge"
    >
      <BaseEdge
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          ...style,
          strokeWidth: isHovered ? 5 : 3,
          stroke: isHovered ? '#6366f1' : '#e2e8f0',
          transition: 'all 0.3s ease'
        }}
        interactionWidth={30}
      />
      <EdgeLabelRenderer>
        <div
          style={{
            position: 'absolute',
            transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
            pointerEvents: 'all',
          }}
          className={`nodrag nopan flex flex-col items-center gap-2 transition-all duration-300 ${isHovered ? 'opacity-100 scale-110' : 'opacity-0 scale-90'}`}
        >
          {label && (
            <div className="px-3 py-1 bg-white border border-indigo-100 rounded-full shadow-lg">
              <span className="text-[9px] font-black uppercase text-indigo-600 tracking-widest">{label}</span>
            </div>
          )}
          <div className="flex gap-2">
            <button
              onClick={onAddClick}
              title="Adicionar Nó aqui"
              className="w-9 h-9 bg-white border-2 border-slate-100 rounded-full flex items-center justify-center text-slate-400 hover:text-indigo-600 hover:border-indigo-400 hover:scale-110 transition-all shadow-xl"
            >
              <Plus size={18} strokeWidth={3} />
            </button>
            <button
              onClick={onDeleteClick}
              title="Remover Conexão"
              className="w-9 h-9 bg-rose-50 border-2 border-rose-100 rounded-full flex items-center justify-center text-rose-400 hover:bg-rose-500 hover:text-white hover:border-rose-500 hover:scale-110 transition-all shadow-xl"
            >
              <Trash2 size={16} strokeWidth={3} />
            </button>
          </div>
        </div>
      </EdgeLabelRenderer>
    </g>
  );
};

// ============================================================================
// CUSTOM NODES
// ============================================================================

const NexusNode = ({ data, selected }: any) => {
  const definition = NODE_LIBRARY.find(n => n.id === data.nodeId) || NODE_LIBRARY[0];
  const nodeOutputs = data.outputs || definition.outputs;
  const Icon = definition.icon;
  const isCondition = definition.type === 'condition';
  const [showPeek, setShowPeek] = useState(false);

  // Status Indicator Mocks (pronto para telemetria real do backend)
  const status = data.status || 'idle'; // 'idle', 'running', 'success', 'error'

  const statusStyles = {
    idle: 'bg-slate-100 text-slate-400 border-slate-200',
    running: 'bg-indigo-100 text-indigo-600 border-indigo-300 animate-pulse',
    success: 'bg-emerald-100 text-emerald-600 border-emerald-300',
    error: 'bg-rose-100 text-rose-600 border-rose-300'
  };

  return (
    <div className={`group relative transition-all duration-500 ${selected ? 'scale-105' : ''}`}>
      {/* Premium Glow */}
      <div className={`absolute -inset-1 rounded-[2.5rem] opacity-0 group-hover:opacity-20 blur-2xl transition-opacity duration-700 ${definition.color}`} />

      {/* Real-time Status Indicator Badge */}
      {status !== 'idle' && (
        <div className={`absolute -top-4 -right-4 z-20 px-3 py-1.5 rounded-xl border-2 font-black text-[9px] uppercase tracking-widest shadow-xl flex items-center gap-2 ${statusStyles[status as keyof typeof statusStyles]}`}>
          {status === 'running' && <div className="w-1.5 h-1.5 bg-indigo-500 rounded-full animate-ping" />}
          {status === 'success' && <ShieldCheck size={12} />}
          {status === 'error' && <X size={12} />}
          {status}
        </div>
      )}

      <div className={`
        relative w-[320px] bg-white rounded-[2.5rem] border-2 shadow-[0_20px_50px_-12px_rgba(0,0,0,0.08)] transition-all duration-500 overflow-hidden
        ${selected ? 'border-indigo-500 shadow-indigo-100 ring-[12px] ring-indigo-50' : 'border-slate-50 shadow-slate-200/40 hover:border-slate-200'}
        ${status === 'running' ? 'ring-4 ring-indigo-200 border-indigo-400' : ''}
        ${status === 'error' ? 'ring-4 ring-rose-100 border-rose-400' : ''}
      `}>
        {/* Input Handles Inteligentes */}
        {definition.inputs.length > 0 && (
          <div className="absolute -top-3 left-0 right-0 flex justify-center gap-6 z-20">
            {definition.inputs.map((inputType, idx) => (
              <div key={idx} className="relative group/handle flex flex-col items-center">
                <div className="absolute -top-8 px-2 py-1 bg-slate-900 text-white rounded-lg text-[8px] font-black uppercase tracking-widest opacity-0 group-hover/handle:opacity-100 transition-opacity whitespace-nowrap shadow-xl pointer-events-none">
                  Expected: {inputType}
                </div>
                <Handle
                  type="target"
                  position={Position.Top}
                  id={`in-${idx}`}
                  className={`!relative !top-0 !transform-none w-6 h-6 border-4 shadow-sm hover:scale-125 transition-all ${TYPE_COLORS[inputType]}`}
                />
              </div>
            ))}
          </div>
        )}

        {/* Node Header */}
        <div className={`px-8 py-5 flex items-center justify-between ${definition.color} text-white relative overflow-hidden group/header`}>
          <div className="absolute top-0 right-0 p-4 opacity-10 rotate-12">
            <Icon size={80} />
          </div>
          <div className="flex items-center gap-4 z-10">
            <div className="p-2.5 bg-white/20 backdrop-blur-md rounded-2xl shadow-inner border border-white/20">
              <Icon size={22} strokeWidth={2.5} />
            </div>
            <div>
              <span className="font-black text-[9px] uppercase tracking-[0.25em] block opacity-70 leading-none mb-1.5">{definition.category}</span>
              <span className="font-black text-sm tracking-tight block uppercase">{definition.label}</span>
            </div>
          </div>
          <button
            onClick={(e) => { e.stopPropagation(); setShowPeek(!showPeek); }}
            className={`z-10 w-8 h-8 rounded-full flex items-center justify-center transition-all shadow-lg border border-white/20 ${showPeek ? 'bg-white text-slate-900' : 'bg-black/20 text-white hover:bg-black/40 opacity-0 group-hover/header:opacity-100'}`}
            title="Data Peek (Previewer)"
          >
            <Eye size={14} />
          </button>
        </div>

        {/* Data Peek Panel */}
        {showPeek && (
          <div className="p-5 bg-slate-900 border-b-2 border-slate-800 animate-in slide-in-from-top-2">
            <div className="flex items-center justify-between mb-2">
              <span className="text-[9px] font-black uppercase tracking-widest text-indigo-400">Payload Schema</span>
              <span className="text-[9px] text-slate-500 font-mono">JSON</span>
            </div>
            <pre className="text-[10px] text-emerald-400 font-mono overflow-x-auto whitespace-pre-wrap">
              {`{
  "trace_id": "uuid-...",
  "status": "success",
  "data": { ... }
}`}
            </pre>
          </div>
        )}
        <div className="p-8 bg-white space-y-5">
          <p className="text-[11px] text-slate-500 font-bold leading-relaxed">{definition.description}</p>

          <div className="pt-5 border-t border-slate-50">
            {data.configSummary ? (
              <div className="px-5 py-4 bg-slate-50 rounded-2xl border border-slate-100 flex items-center justify-between group/cfg cursor-pointer hover:bg-slate-100 transition-colors">
                <span className="text-[10px] font-mono text-slate-600 truncate max-w-[200px]">{data.configSummary}</span>
                <Settings2 size={14} className="text-slate-300 group-hover/cfg:rotate-90 transition-transform" />
              </div>
            ) : (
              <div className="px-5 py-4 bg-indigo-50/40 rounded-2xl border border-indigo-100/50 flex items-center justify-between animate-pulse">
                <span className="text-[10px] font-black text-indigo-400 uppercase tracking-widest italic">Aguardando Parâmetros</span>
                <div className="flex gap-1.5">
                  {[1, 2, 3].map(i => <div key={i} className="w-1.5 h-1.5 rounded-full bg-indigo-200" />)}
                </div>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* ── HANDLES DE CONDIÇÃO (dentro do card para ReactFlow funcionar) ─────────────── */}
      {/* Os handles precisam estar dentro do nó principal. Drag para criar conexão. */}
      {/* Posicionados na borda inferior do card, com antenas visuais abaixo */}
      {isCondition && (() => {
        const outputs = data.outputs || definition.outputs;
        const total = outputs.length;
        return outputs.map((_: any, idx: number) => {
          const isElse = idx === total - 1;
          const handleId = isElse ? 'else' : `route_${idx}`;
          const leftPct = total === 1 ? 50 : ((idx + 1) / (total + 1)) * 100;
          return (
            <div
              key={`${handleId}-container`}
              className="absolute z-15"
              style={{
                bottom: -8,
                left: `${leftPct}%`,
                transform: 'translateX(-50%)',
                width: 20,
                height: 20,
              }}
              onClick={(e) => {
                e.stopPropagation();
                if (data.onAddFromAntenna) data.onAddFromAntenna(data.id, handleId);
              }}
            >
              <Handle
                key={`${handleId}-${data._handleKey || 0}`}
                type="source"
                position={Position.Bottom}
                id={handleId}
                className="!w-full !h-full !border-4 !shadow-sm hover:!scale-150 hover:!border-indigo-500 hover:!shadow-lg transition-all"
                style={{
                  background: isElse ? '#fff1f2' : '#eef2ff',
                  borderColor: isElse ? '#fb7185' : '#818cf8',
                  cursor: 'crosshair',
                }}
              />
            </div>
          );
        });
      })()}

      {/* ── ANTENAS VISUAIS (apenas CSS, sem handles) ─────────────── */}
      {/* Hastes que conectam o card aos handles/bolinhas */}
      {isCondition && (() => {
        const outputs = data.outputs || definition.outputs;
        const routes = data.config?.routes || [];
        const total = outputs.length;
        const cardWidth = 320;

        return (
          <div
            className="pointer-events-none"
            style={{ position: 'absolute', bottom: -8, left: 0, width: cardWidth, height: 60, zIndex: 10 }}
          >
            {outputs.map((_: any, idx: number) => {
              const isElse = idx === total - 1;
              const handleId = isElse ? 'else' : `route_${idx}`;
              const label = isElse ? 'ELSE' : (routes[idx]?.label || `ROTA ${idx}`);
              const leftPct = total === 1 ? 50 : ((idx + 1) / (total + 1)) * 100;
              const leftPx = (cardWidth * leftPct) / 100;

              return (
                <div
                  key={`antenna-${handleId}`}
                  className="absolute top-0 flex flex-col items-center"
                  style={{ left: leftPx, transform: 'translateX(-50%)' }}
                >
                  {/* Label da rota */}
                  <div className={`absolute -top-5 text-[8px] font-black uppercase tracking-widest whitespace-nowrap select-none ${isElse ? 'text-rose-400' : 'text-indigo-400'}`}>
                    {label}
                  </div>

                  {/* Haste - conecta o handle ao próximo nó */}
                  <div className={`w-px h-12 ${isElse ? 'bg-rose-200' : 'bg-indigo-200'}`} />
                </div>
              );
            })}
          </div>
        );
      })()}

      {/* ANTENA / TRAÇO INFERIOR (Apenas para nós que não sejam Logic Router) */}
      {!isCondition && (
        <div className="absolute left-1/2 -translate-x-1/2 flex flex-col items-center pointer-events-none">
          <div className="w-0.5 h-12 bg-slate-200 group-hover:bg-indigo-300 transition-colors" />
          <div className="relative w-5 h-5 flex items-center justify-center pointer-events-auto">
            <div
              className="absolute inset-0 bg-white border-4 border-slate-200 rounded-full cursor-crosshair hover:scale-150 hover:border-indigo-500 hover:shadow-lg transition-all flex items-center justify-center group/antenna"
              onClick={(e) => {
                e.stopPropagation();
                if (data.onAddFromAntenna) data.onAddFromAntenna(data.id, 'antenna-source');
              }}
            >
              <div className="w-1.5 h-1.5 bg-slate-400 rounded-full group-hover/antenna:bg-indigo-500 transition-colors" />
            </div>
            <Handle
              type="source"
              position={Position.Bottom}
              id="antenna-source"
              className="!opacity-0 !w-5 !h-5 !border-0 !bg-transparent !min-w-0 !min-h-0"
            />
          </div>
        </div>
      )}
    </div>
  );
};

// ============================================================================
// MODAL: NODE LIBRARY
// ============================================================================

const NodeLibraryModal = ({ isOpen, onClose, onSelect, hasTrigger }: { isOpen: boolean, onClose: () => void, onSelect: (node: NodeDefinition) => void, hasTrigger: boolean }) => {
  const [search, setSearch] = useState('');

  const filtered = NODE_LIBRARY.filter(n => {
    // Melhoria: Não mostrar gatilhos se já houver um gatilho no canvas
    if (hasTrigger && n.type === 'trigger') return false;

    return (
      n.label.toLowerCase().includes(search.toLowerCase()) ||
      n.category.toLowerCase().includes(search.toLowerCase())
    );
  });

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center p-6 backdrop-blur-2xl bg-slate-900/50 animate-in fade-in duration-500">
      <div className="bg-white/95 glass w-full max-w-xl rounded-[3.5rem] shadow-[0_60px_120px_-20px_rgba(0,0,0,0.4)] border border-white/50 overflow-hidden flex flex-col max-h-[650px] animate-in zoom-in-95 duration-300">
        {/* Header */}
        <div className="px-12 py-10 border-b border-slate-100 flex items-center justify-between bg-white/50">
          <div>
            <h3 className="text-3xl font-black text-slate-900 tracking-tighter">Nexus Architect</h3>
            <p className="text-[11px] text-slate-500 font-bold uppercase tracking-[0.3em] mt-1.5 opacity-60">Escolha o próximo degrau da sua inteligência</p>
          </div>
          <button onClick={onClose} className="w-14 h-14 hover:bg-slate-100 rounded-full flex items-center justify-center transition-all text-slate-400 hover:text-slate-900">
            <X size={28} />
          </button>
        </div>

        {/* Search */}
        <div className="px-12 py-8">
          <div className="relative group">
            <div className="absolute -inset-1.5 bg-gradient-to-r from-indigo-500 via-purple-500 to-pink-500 rounded-3xl blur opacity-10 group-focus-within:opacity-30 transition duration-700" />
            <div className="relative flex items-center">
              <Search className="absolute left-6 text-slate-300 group-focus-within:text-indigo-500 transition-colors" size={22} />
              <input
                autoFocus
                type="text"
                placeholder="O que sua automação deve fazer agora?..."
                className="w-full bg-slate-50 border-2 border-slate-100 rounded-[2rem] py-6 pl-16 pr-8 text-sm font-black text-slate-800 placeholder:text-slate-300 outline-none focus:border-indigo-400 focus:bg-white transition-all shadow-inner"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
          </div>
        </div>

        {/* List */}
        <div className="flex-1 overflow-y-auto px-10 pb-12 custom-scrollbar">
          <div className="grid grid-cols-1 gap-4">
            {filtered.map(node => (
              <button
                key={node.id}
                onClick={() => onSelect(node)}
                className="group flex items-center gap-8 p-6 rounded-[2.5rem] hover:bg-white hover:shadow-2xl hover:shadow-indigo-100 transition-all border-2 border-transparent hover:border-indigo-50 text-left relative overflow-hidden"
              >
                <div className={`relative z-10 w-20 h-20 rounded-3xl ${node.color} text-white shadow-2xl flex items-center justify-center group-hover:scale-110 transition-transform duration-700`}>
                  <node.icon size={32} strokeWidth={2.5} />
                </div>
                <div className="flex-1 z-10">
                  <div className="flex items-center gap-3 mb-1.5">
                    <span className="text-base font-black text-slate-900 uppercase tracking-tight">{node.label}</span>
                    <span className="text-[10px] px-3 py-1 bg-slate-100 text-slate-400 rounded-full font-black uppercase tracking-widest">{node.category}</span>
                  </div>
                  <p className="text-xs text-slate-400 font-bold leading-relaxed">{node.description}</p>
                </div>
                <div className="z-10 w-12 h-12 bg-slate-50 rounded-full flex items-center justify-center text-slate-200 group-hover:bg-indigo-600 group-hover:text-white group-hover:scale-110 transition-all duration-300">
                  <Plus size={20} strokeWidth={3} />
                </div>
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
};


// ============================================================================
// COMPONENT: JSON VARIABLE PICKER (TREE VIEW)
// ============================================================================

const JSONVariablePicker = ({ data, onSelect, path = '', level = 0 }: { data: any, onSelect: (path: string, key: string) => void, path?: string, level?: number }) => {
  if (data === null || data === undefined) return <span className="text-slate-400">null</span>;

  const isObject = typeof data === 'object' && !Array.isArray(data);
  const isArray = Array.isArray(data);

  if (isObject) {
    const keys = Object.keys(data);
    return (
      <div className="font-mono text-[10px] leading-relaxed">
        <span className="text-slate-400">{level > 0 ? '{' : ''}</span>
        <div className={level > 0 ? 'ml-4 border-l border-slate-100 pl-3' : ''}>
          {keys.map((key, idx) => {
            const currentPath = path ? `${path}.${key}` : key;
            const value = data[key];
            const isValObj = typeof value === 'object' && value !== null;

            return (
              <div key={key} className="group/json-line transition-colors">
                <div
                  className="flex items-center gap-1 cursor-pointer hover:bg-indigo-50/50 rounded px-1 -ml-1 py-0.5 transition-all"
                  onClick={(e) => {
                    e.stopPropagation();
                    if (!isValObj) onSelect(currentPath, key);
                  }}
                >
                  <span className="text-indigo-600 font-bold">"{key}"</span>
                  <span className="text-slate-400">:</span>
                  {!isValObj && (
                    <span className="text-emerald-600 ml-1">
                      {typeof value === 'string' ? `"${value}"` : String(value)}
                      {idx < keys.length - 1 ? ',' : ''}
                    </span>
                  )}
                  {isValObj && <span className="text-slate-400">{Array.isArray(value) ? '[' : '{'}</span>}
                </div>
                {isValObj && (
                  <JSONVariablePicker
                    data={value}
                    onSelect={onSelect}
                    path={currentPath}
                    level={level + 1}
                  />
                )}
              </div>
            );
          })}
        </div>
        <span className="text-slate-400">{level > 0 ? '}' : ''}{level > 0 && ','}</span>
      </div>
    );
  }

  if (isArray) {
    return (
      <div className="font-mono text-[10px] leading-relaxed ml-4 border-l border-slate-100 pl-3">
        {data.map((item, idx) => {
          const currentPath = `${path}[${idx}]`;
          const isItemObj = typeof item === 'object' && item !== null;

          return (
            <div key={idx} className="group/json-line transition-colors">
              {!isItemObj ? (
                <div
                  className="cursor-pointer hover:bg-indigo-50/50 rounded px-1 -ml-1 py-0.5 transition-all"
                  onClick={() => onSelect(currentPath, `item_${idx}`)}
                >
                  <span className="text-emerald-600">
                    {typeof item === 'string' ? `"${item}"` : String(item)}
                    {idx < data.length - 1 ? ',' : ''}
                  </span>
                </div>
              ) : (
                <JSONVariablePicker
                  data={item}
                  onSelect={onSelect}
                  path={currentPath}
                  level={level + 1}
                />
              )}
            </div>
          );
        })}
        <span className="text-slate-400">]</span>
      </div>
    );
  }

  return <span className="text-emerald-600">{String(data)}</span>;
};

// ============================================================================
// DRAWER: CONFIGURATION
// ============================================================================

const ConfigDrawer = ({ node, allNodes, projectId, onUpdate, onDelete, onClose, testLogs }: { node: Node | null, allNodes: Node[], projectId?: string, onUpdate: (id: string, data: any) => void, onDelete: (id: string) => void, onClose: () => void, testLogs: any[] }) => {
  const [nodeLabel, setNodeLabel] = useState('');
  const [mapperValue, setMapperValue] = useState('');
  const [schemas, setSchemas] = useState<any[]>([]);
  const [tables, setTables] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedSchema, setSelectedSchema] = useState('');
  const [selectedTable, setSelectedTable] = useState('');
  const [selectedEvent, setSelectedEvent] = useState('*');
  const [filterMode, setFilterMode] = useState<'no-code' | 'code'>('no-code');
  const [mappingMode, setMappingMode] = useState<'no-code' | 'code'>('no-code');
  const [filterRows, setFilterRows] = useState<any[]>([{ column: '', operator: '==', value: '' }]);
  const [mappingRows, setMappingRows] = useState<any[]>([]);
  const [manualFilter, setManualFilter] = useState('');
  const [columns, setColumns] = useState<any[]>([]);
  const [enums, setEnums] = useState<any[]>([]);
  const [dbActionType, setDbActionType] = useState('INSERT');
  const [isPickerOpen, setIsPickerOpen] = useState(false);
  const [pickerCallback, setPickerCallback] = useState<(v: string) => void>(() => { });
  const [isPipePickerOpen, setIsPipePickerOpen] = useState(false);
  const [pipePickerCallback, setPipePickerCallback] = useState<(pipe: string) => void>(() => { });
  const [logicRoutes, setLogicRoutes] = useState<any[]>([]);

  // Webhook specific states
  const [webhookPath, setWebhookPath] = useState('');
  const [webhookMethod, setWebhookMethod] = useState('POST');
  const [webhookAsyncResponse, setWebhookAsyncResponse] = useState('');
  const [projectData, setProjectData] = useState<any>(null);
  const [copiedCurl, setCopiedCurl] = useState(false);
  const [securityLevel, setSecurityLevel] = useState('L1');

  // Response node specific states
  const [responseType, setResponseType] = useState('json');

  // Auth Policy Builder (replaces old webhookAuth/webhookSecret)
  const [authPolicies, setAuthPolicies] = useState<any[]>([]);
  const [availableAppClients, setAvailableAppClients] = useState<any[]>([]);
  const [availableKeyGroups, setAvailableKeyGroups] = useState<any[]>([]);

  // Helpers for Auth Policy Builder
  const hasPolicy = (method: string) => authPolicies.some(p => p.method === method);
  const getPolicyConfig = (method: string) => authPolicies.find(p => p.method === method)?.config || {};
  const togglePolicy = (method: string) => {
    if (method === 'none') {
      setAuthPolicies([]);
      return;
    }
    if (hasPolicy(method)) {
      setAuthPolicies(prev => prev.filter(p => p.method !== method));
    } else {
      const defaultConfig: any = {};
      if (method === 'anonymous') defaultConfig.app_client_ids = [];
      if (method === 'anonymous') defaultConfig.allow_project_key = true;
      if (method === 'api_key') defaultConfig.group_ids = [];
      if (method === 'identity') defaultConfig.min_role = 'authenticated';
      if (['bearer', 'hmac_sha256', 'rsa_signature', 'basic_auth'].includes(method)) defaultConfig.vault_ref = '';
      setAuthPolicies(prev => [...prev, { method, config: defaultConfig }]);
    }
  };
  const updatePolicyConfig = (method: string, updates: any) => {
    setAuthPolicies(prev => prev.map(p => p.method === method ? { ...p, config: { ...p.config, ...updates } } : p));
  };

  const flattenObject = useCallback((obj: any, prefix = ''): string[] => {
    if (!obj || typeof obj !== 'object') return [];
    return Object.keys(obj).reduce((acc: string[], k) => {
      const pre = prefix.length ? prefix + '.' : '';
      if (typeof obj[k] === 'object' && obj[k] !== null && !Array.isArray(obj[k])) {
        // Recursivo para objetos, mas não adicionamos o nó pai como sugestão mapeável
        acc.push(...flattenObject(obj[k], pre + k));
      } else {
        // Apenas nós folha são adicionados como sugestões de mapeamento
        acc.push(pre + k);
      }
      return acc;
    }, []);
  }, []);

  const detectedPayload = useMemo(() => {
    if (!testLogs || testLogs.length === 0) return null;
    // Try to find trigger/webhook logs first, but fall back to any log with data
    const triggerLog = testLogs.find(log =>
      log.node_id?.includes('trigger') ||
      log.node_name?.toLowerCase().includes('webhook') ||
      log.node_name?.toLowerCase().includes('gatilho')
    );

    // If no trigger found, look for any log with output_data or input_data (including Meta requests)
    const anyLogWithData = testLogs.find(log =>
      log.output_data || log.input_data
    ) || testLogs[0];

    const selectedLog = triggerLog || anyLogWithData;
    return selectedLog?.output_data || selectedLog?.input_data || null;
  }, [testLogs]);

  const detectedFields = useMemo(() => {
    if (!detectedPayload || typeof detectedPayload !== 'object') return [];
    return flattenObject(detectedPayload);
  }, [detectedPayload, flattenObject]);

  useEffect(() => {
    const fetchProjectData = async () => {
      if (!projectId) return;
      try {
        const token = localStorage.getItem('cascata_token');
        const res = await fetch('/api/control/projects', {
          headers: { 'Authorization': `Bearer ${token}` }
        });
        if (res.ok) {
          const projects = await res.json();
          const current = projects.find((p: any) => p.slug === projectId);
          setProjectData(current);
        }
      } catch (err) {
        console.error('[NexusArchitect] Failed to fetch project data:', err);
      }
    };
    fetchProjectData();
  }, [projectId]);

  // Sincronizar com o nó mais recente do estado global
  const syncedNode = node ? allNodes.find((n: Node) => n.id === node.id) || node : null;
  const definition = syncedNode ? NODE_LIBRARY.find(n => n.id === syncedNode.data.nodeId) : null;

  // Fetch App Clients & Key Groups for Auth Policy Builder
  useEffect(() => {
    if (syncedNode && projectId && definition?.id === 'webhook_trigger') {
      // App Clients: extraídos do projectData (metadata.app_clients)
      // Não existe rota /api/data/{slug}/app-clients — os app_clients vivem no metadata do projeto
      if (projectData?.metadata?.app_clients) {
        setAvailableAppClients(projectData.metadata.app_clients);
      } else {
        setAvailableAppClients([]);
      }

      // Key Groups: rota de segurança funcional
      const token = localStorage.getItem('cascata_token');
      fetch(`/api/data/${projectId}/security/key-groups`, {
        headers: { 'Authorization': `Bearer ${token}` }
      })
        .then(res => res.ok ? res.json() : [])
        .then(data => setAvailableKeyGroups(Array.isArray(data) ? data : []))
        .catch(() => setAvailableKeyGroups([]));
    }
  }, [syncedNode?.id, projectId, definition?.id, projectData]);

  useEffect(() => {
    if (syncedNode) {
      const currentNode = syncedNode; // Usar o nó sincronizado

      setNodeLabel(currentNode.data.label);
      setMapperValue(currentNode.data.config?.mapping || '');
      setSelectedSchema(currentNode.data.config?.schema || '');
      setSelectedTable(currentNode.data.config?.table || '');
      setSelectedEvent(currentNode.data.config?.event || '*');
      setLogicRoutes(currentNode.data.config?.routes || []);

      const savedFilters = currentNode.data.config?.filterRows;
      if (savedFilters && Array.isArray(savedFilters)) {
        setFilterRows(savedFilters);
        setFilterMode('no-code');
      } else if (currentNode.data.config?.filter) {
        setManualFilter(currentNode.data.config.filter);
        setFilterMode('code');
      }

      const savedMapping = currentNode.data.config?.mappingRows;
      if (savedMapping && Array.isArray(savedMapping)) {
        setMappingRows(savedMapping);
        setMappingMode('no-code');
      } else if (currentNode.data.config?.mapping) {
        let val = currentNode.data.config.mapping;
        if (typeof val === 'object') {
          val = JSON.stringify(val, null, 2);
        }
        setMapperValue(val);
        setMappingMode('code');
      }

      setDbActionType(currentNode.data.config?.operation || 'INSERT');
      setMappingMode(currentNode.data.config?.mappingMode || 'no-code');

      // Webhook Sync
      setWebhookPath(currentNode.data.config?.path_slug || '');
      setAuthPolicies(currentNode.data.config?.auth_policies || []);
      setWebhookMethod(currentNode.data.config?.method || 'POST');
      setWebhookAsyncResponse(currentNode.data.config?.async_response || '');
      setSecurityLevel(currentNode.data.config?.securityLevel || 'L1');

      // Response node sync
      setResponseType(currentNode.data.config?.responseType || 'json');
    }
  }, [syncedNode?.id, syncedNode?.data]);

  // Fetch Enums (Global)
  useEffect(() => {
    if (syncedNode && projectId && enums.length === 0) {
      fetch(`/api/data/${projectId}/enum-types`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      })
        .then(res => res.json())
        .then(data => setEnums(Array.isArray(data) ? data : []));
    }
  }, [node, projectId, enums.length]);

  // Fetch Schemas
  useEffect(() => {
    const isDBNode = definition?.id === 'pre_event_trigger' || definition?.id === 'post_event_trigger' || definition?.id === 'database_action';
    if (syncedNode && isDBNode && projectId && schemas.length === 0) {
      setLoading(true);
      fetch(`/api/data/${projectId}/schemas`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      })
        .then(res => res.json())
        .then(data => setSchemas(Array.isArray(data) ? data : []))
        .finally(() => setLoading(false));
    }
  }, [node, definition, projectId, schemas.length]);

  // Fetch Tables
  useEffect(() => {
    if (selectedSchema && projectId) {
      setLoading(true);
      fetch(`/api/data/${projectId}/tables?schema=${selectedSchema}`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      })
        .then(res => res.json())
        .then(data => setTables(Array.isArray(data) ? data : []))
        .finally(() => setLoading(false));
    }
  }, [selectedSchema, projectId]);

  // Fetch Columns
  useEffect(() => {
    if (selectedTable && selectedSchema && projectId) {
      setLoading(true);
      fetch(`/api/data/${projectId}/tables/${selectedTable}/columns?schema=${selectedSchema}`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      })
        .then(res => res.json())
        .then(data => setColumns(Array.isArray(data) ? data : []))
        .finally(() => setLoading(false));
    }
  }, [selectedTable, selectedSchema, projectId]);

  const handleSave = () => {
    if (!syncedNode) return;

    // Preservar o config existente do HTTPNodeSimple se for nó http_request
    const existingHttpConfig = syncedNode.data.config?.url ? {
      url: syncedNode.data.config.url,
      method: syncedNode.data.config.method,
      headers: syncedNode.data.config.headers,
      queryParams: syncedNode.data.config.queryParams,
      bodyType: syncedNode.data.config.bodyType,
      bodyJSON: syncedNode.data.config.bodyJSON,
      bodyRaw: syncedNode.data.config.bodyRaw,
      authType: syncedNode.data.config.authType,
      authConfig: syncedNode.data.config.authConfig,
      timeout: syncedNode.data.config.timeout,
      retryCount: syncedNode.data.config.retryCount,
      followRedirects: syncedNode.data.config.followRedirects,
      validateSSL: syncedNode.data.config.validateSSL,
      responseFormat: syncedNode.data.config.responseFormat,
      outputMappings: syncedNode.data.config.outputMappings
    } : null;

    const config = {
      ...(syncedNode.data.config || {}),
      ...(existingHttpConfig || {}),
      schema: selectedSchema,
      table: selectedTable,
      event: selectedEvent,
      columns,
      filter: filterMode === 'code' ? manualFilter : filterRows.map(r => `${r.column} ${r.operator} '${r.value}'`).join(' AND '),
      filterRows: filterMode === 'no-code' ? filterRows : null,
      operation: dbActionType,
      mapping: mappingMode === 'code' ? mapperValue : mappingRows.reduce((acc, r) => ({ ...acc, [r.column]: r.value }), {}),
      mappingRows: mappingMode === 'no-code' ? mappingRows : null,
      mappingMode,
      routes: logicRoutes,
      // Webhook persistent config
      path_slug: webhookPath,
      auth_policies: authPolicies,
      method: webhookMethod,
      async_response: webhookAsyncResponse,
      securityLevel: securityLevel
    };

    // Update node definition outputs if logic node
    // Sempre: N rotas configuradas + 1 else = N+1 outputs. Mínimo: 2 (1 rota + else)
    const dynamicOutputs = definition?.id === 'condition_if'
      ? [...(logicRoutes.length > 0 ? logicRoutes.map(() => 'any') : ['any']), 'any']
      : (definition?.outputs || []);

    onUpdate(syncedNode.id, {
      ...syncedNode.data,
      label: nodeLabel,
      config,
      outputs: dynamicOutputs,
      configSummary: definition?.id === 'step_up_challenge_trigger'
        ? `Provider: ${config.provider || '*'}`
        : (logicRoutes.length > 0 ? `${logicRoutes.length} Rotas` : (selectedTable ? `${selectedEvent} on ${selectedTable}` : (mapperValue ? 'Mapped' : '')))
    });
    onClose();
  };

  if (!node) return null;

  const availableNodes = allNodes
    .filter(n => n.id !== syncedNode.id && n.position.y <= syncedNode.position.y)
    .map(n => {
      // Default schema for every node
      const schema: Record<string, string> = {
        'payload': 'object',
        'status': 'string'
      };

      // PADRONIZAÇÃO DE SEQUESTRO (Sinergia)
      // Se for um nó de trigger de banco, expomos os campos estruturados que agora existem no backend
      const isNexusTrigger = n.data.nodeId === 'pre_event_trigger' || n.data.nodeId === 'post_event_trigger';
      if (isNexusTrigger) {
        schema['out.data.original_request'] = 'object';
        schema['out.data.db_result'] = 'object';
      }

      if (n.data.nodeId === 'step_up_challenge_trigger') {
        schema['otp.code'] = 'string';
        schema['otp.provider'] = 'string';
        schema['user.id'] = 'string';
        schema['user.identifier'] = 'string';
        schema['challenge.id'] = 'string';
      }

      // Se for um nó de banco (Trigger ou Action) com colunas persistidas, expomos elas
      if (n.data.config?.columns && Array.isArray(n.data.config.columns)) {
        n.data.config.columns.forEach((c: any) => {
          // Usamos 'out.data' para alinhar com a estrutura interna do NexusEngine
          // No backend, o NexusState remove o prefixo out.data. para acessar o payload do trigger
          schema[`out.data.${c.name}`] = c.type || 'any';
        });
      }

      // If it's an HTTP node with output mappings, expose mapped response fields
      if (n.data.nodeId === 'http_request' && n.data.config?.outputMappings && Array.isArray(n.data.config.outputMappings)) {
        n.data.config.outputMappings.forEach((m: any) => {
          schema[`body.${m.outputName}`] = m.outputType || 'any';
        });
      }

      return {
        id: n.id,
        label: n.data.label,
        nodeId: n.data.nodeId,
        type: n.data.type,
        schema
      };
    });

  const isTrigger = definition?.type === 'trigger';
  const isDBNode = definition?.id === 'pre_event_trigger' || definition?.id === 'post_event_trigger' || definition?.id === 'database_action';

  return (
    <div className="absolute top-0 right-0 h-full w-[500px] bg-white border-l border-slate-100 shadow-[0_0_120px_rgba(0,0,0,0.15)] z-50 flex flex-col animate-in slide-in-from-right duration-700">
      <div className="p-12 border-b border-slate-50 flex items-center justify-between bg-slate-50/40 backdrop-blur-xl">
        <div className="flex items-center gap-5">
          <div className={`w-16 h-16 rounded-3xl ${definition?.color} text-white shadow-2xl flex items-center justify-center border-4 border-white/20`}>
            {definition && <definition.icon size={28} strokeWidth={2.5} />}
          </div>
          <div>
            <h3 className="text-2xl font-black text-slate-900 tracking-tighter uppercase leading-none">{definition?.label || 'Setup Nó'}</h3>
            <p className="text-[10px] text-slate-400 font-black uppercase tracking-[0.3em] mt-2.5 flex items-center gap-2">
              <Cpu size={12} className="text-indigo-500" /> Nexus Core v0.1.0
            </p>
          </div>
        </div>
        <button onClick={onClose} className="w-14 h-14 hover:bg-slate-100 rounded-2xl flex items-center justify-center transition-all text-slate-400">
          <X size={28} />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-12 space-y-12 custom-scrollbar">
        <div className="space-y-8">
          <div className="space-y-4">
            <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.3em] flex items-center gap-2">
              <Settings2 size={14} /> Título do Nó
            </label>
            <input
              type="text"
              className="w-full bg-slate-50 border-2 border-slate-100 rounded-[2rem] px-8 py-5 text-sm font-black text-slate-800 focus:border-indigo-500 focus:bg-white transition-all outline-none shadow-inner"
              placeholder="Ex: Main Auth Gate"
              value={nodeLabel}
              onChange={(e) => setNodeLabel(e.target.value)}
            />
          </div>

          {/* Security Gateway Panel */}
          <div className="space-y-4 pt-6 border-t border-slate-100">
            <div className="flex items-center justify-between">
              <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.3em] flex items-center gap-2">
                <ShieldCheck size={14} className={securityLevel === 'L1' ? 'text-emerald-500' : securityLevel === 'L2' ? 'text-amber-500' : 'text-rose-500'} /> Security Gateway
              </label>
              <span className={`text-[8px] px-2 py-0.5 rounded font-black uppercase tracking-widest ${securityLevel === 'L1' ? 'bg-emerald-50 text-emerald-600' : securityLevel === 'L2' ? 'bg-amber-50 text-amber-600' : 'bg-rose-50 text-rose-600'}`}>
                {securityLevel === 'L1' ? 'Standard' : securityLevel === 'L2' ? 'Strict' : 'Admin'}
              </span>
            </div>
            <div className="grid grid-cols-3 gap-3">
              {[
                { id: 'L1', label: 'L1', desc: 'Standard', icon: Globe, color: 'emerald' },
                { id: 'L2', label: 'L2', desc: 'Strict', icon: UserCheck, color: 'amber' },
                { id: 'L3', label: 'L3', desc: 'Admin', icon: Lock, color: 'rose' }
              ].map(level => (
                <button
                  key={level.id}
                  onClick={() => setSecurityLevel(level.id)}
                  className={`flex flex-col items-center gap-2 p-4 rounded-2xl border-2 transition-all group ${securityLevel === level.id ? `bg-${level.color}-50 border-${level.color}-500 text-${level.color}-600 shadow-lg shadow-${level.color}-100 scale-105` : 'bg-slate-50 border-slate-100 text-slate-400 hover:border-slate-200'}`}
                >
                  <level.icon size={16} className={securityLevel === level.id ? `text-${level.color}-500` : 'text-slate-300 group-hover:text-slate-400'} />
                  <div className="text-center">
                    <span className="text-[9px] font-black block leading-none">{level.label}</span>
                    <span className="text-[6px] font-bold uppercase tracking-widest opacity-60">{level.desc}</span>
                  </div>
                </button>
              ))}
            </div>
            <p className="text-[9px] text-slate-400 font-bold italic opacity-60 leading-relaxed px-1">
              {securityLevel === 'L1' ? 'Acesso público ou anônimo permitido para este nó.' :
                securityLevel === 'L2' ? 'Exige obrigatoriamente um usuário autenticado (User+).' :
                  'Acesso restrito apenas a Administradores ou Service Keys.'}
            </p>
          </div>

          {isDBNode && (
            <div className="space-y-6 pt-6 border-t border-slate-100">
              {definition?.id === 'database_action' && (
                <div className="space-y-3">
                  <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Operação</label>
                  <div className="grid grid-cols-3 gap-2">
                    {['INSERT', 'UPDATE', 'DELETE'].map(op => (
                      <button
                        key={op}
                        onClick={() => setDbActionType(op)}
                        className={`py-3 rounded-xl border-2 font-black text-[9px] transition-all ${dbActionType === op ? 'bg-emerald-50 border-emerald-500 text-emerald-600' : 'bg-slate-50 border-slate-100 text-slate-400'}`}
                      >
                        {op}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              {isTrigger && (
                <div className="space-y-3">
                  <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Gatilho de Evento</label>
                  <div className="grid grid-cols-3 gap-2">
                    {['*', 'INSERT', 'UPDATE', 'DELETE', 'SELECT'].map(ev => (
                      <button
                        key={ev}
                        onClick={() => setSelectedEvent(ev)}
                        className={`py-3 rounded-xl border-2 font-black text-[9px] transition-all ${selectedEvent === ev ? 'bg-rose-50 border-rose-500 text-rose-600' : 'bg-slate-50 border-slate-100 text-slate-400'}`}
                      >
                        {ev === '*' ? 'TODOS (*)' : ev}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-3">
                  <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Schema</label>
                  <select
                    className="w-full bg-slate-50 border-2 border-slate-100 rounded-xl px-5 py-4 text-xs font-bold text-slate-800 outline-none focus:border-indigo-400"
                    value={selectedSchema}
                    onChange={(e) => setSelectedSchema(e.target.value)}
                  >
                    <option value="">Schema...</option>
                    {schemas.map(s => <option key={s.name} value={s.name}>{s.name}</option>)}
                  </select>
                </div>
                <div className="space-y-3">
                  <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Tabela</label>
                  <select
                    disabled={!selectedSchema}
                    className="w-full bg-slate-50 border-2 border-slate-100 rounded-xl px-5 py-4 text-xs font-bold text-slate-800 outline-none focus:border-indigo-400 disabled:opacity-50"
                    value={selectedTable}
                    onChange={(e) => {
                      setSelectedTable(e.target.value);
                      setColumns([]); // Reset columns on table change
                    }}
                  >
                    <option value="">Tabela...</option>
                    {tables.map(t => <option key={t.name} value={t.name}>{t.name}</option>)}
                  </select>
                </div>
              </div>

              {isTrigger && (
                <div className="space-y-4">
                  <div className="flex items-center justify-between">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Filtro Condicional</label>
                    <div className="flex bg-slate-100 p-1 rounded-lg gap-1">
                      <button
                        onClick={() => setFilterMode('no-code')}
                        className={`px-3 py-1 rounded-md text-[8px] font-black uppercase transition-all ${filterMode === 'no-code' ? 'bg-white shadow-sm text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                      >
                        No-Code
                      </button>
                      <button
                        onClick={() => setFilterMode('code')}
                        className={`px-3 py-1 rounded-md text-[8px] font-black uppercase transition-all ${filterMode === 'code' ? 'bg-white shadow-sm text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                      >
                        Code
                      </button>
                    </div>
                  </div>

                  {filterMode === 'no-code' ? (
                    <div className="space-y-3">
                      {filterRows.map((row, idx) => (
                        <div key={idx} className="flex gap-2 items-center animate-in fade-in slide-in-from-left-2">
                          <select
                            className="flex-1 bg-slate-50 border-2 border-slate-100 rounded-xl px-3 py-3 text-[10px] font-bold outline-none focus:border-indigo-400"
                            value={row.column}
                            onChange={(e) => {
                              const newRows = [...filterRows];
                              newRows[idx].column = e.target.value;
                              setFilterRows(newRows);
                            }}
                          >
                            <option value="">Coluna...</option>
                            {columns.map(c => <option key={c.name} value={c.name}>{c.name}</option>)}
                          </select>
                          <select
                            className="w-20 bg-slate-50 border-2 border-slate-100 rounded-xl px-2 py-3 text-[10px] font-bold outline-none focus:border-indigo-400"
                            value={row.operator}
                            onChange={(e) => {
                              const newRows = [...filterRows];
                              newRows[idx].operator = e.target.value;
                              setFilterRows(newRows);
                            }}
                          >
                            <option value="==">==</option>
                            <option value="!=">!=</option>
                            <option value=">">&gt;</option>
                            <option value="<">&lt;</option>
                            <option value="in">está em (IN)</option>
                            <option value="not_in">não está em (NOT IN)</option>
                            <option value="contains">contém</option>
                            <option value="not_contains">não contém</option>
                          </select>
                          <div className="flex-1 relative group/input">
                            <input
                              type="text"
                              className="w-full bg-slate-50 border-2 border-slate-100 rounded-xl px-3 py-3 pr-8 text-[10px] font-bold outline-none focus:border-indigo-400"
                              placeholder="Valor..."
                              value={row.value}
                              onChange={(e) => {
                                const newRows = [...filterRows];
                                newRows[idx].value = e.target.value;
                                setFilterRows(newRows);
                              }}
                            />
                            <button
                              onClick={() => {
                                setPickerCallback(() => (val: string) => {
                                  const newRows = [...filterRows];
                                  newRows[idx].value = `{{${val}}}`;
                                  setFilterRows(newRows);
                                });
                                setIsPickerOpen(true);
                              }}
                              className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-300 hover:text-indigo-500 opacity-0 group-hover/input:opacity-100 transition-all"
                            >
                              <Plus size={14} />
                            </button>
                          </div>
                          <button
                            onClick={() => setFilterRows(filterRows.filter((_, i) => i !== idx))}
                            className="w-8 h-8 flex items-center justify-center text-rose-300 hover:text-rose-500 transition-colors"
                          >
                            <Trash2 size={14} />
                          </button>
                        </div>
                      ))}
                      <button
                        onClick={() => setFilterRows([...filterRows, { column: '', operator: '==', value: '' }])}
                        className="w-full py-2 border-2 border-dashed border-slate-200 rounded-xl text-[9px] font-black text-slate-400 hover:border-indigo-300 hover:text-indigo-500 transition-all uppercase tracking-widest flex items-center justify-center gap-2"
                      >
                        <Plus size={12} /> Add Condição
                      </button>
                    </div>
                  ) : (
                    <textarea
                      value={manualFilter}
                      onChange={(e) => setManualFilter(e.target.value)}
                      className="w-full h-32 bg-slate-900 border-2 border-slate-800 rounded-2xl p-5 text-xs font-mono text-emerald-400 outline-none focus:border-indigo-500 shadow-inner"
                      placeholder="Ex: status == 'active' && price > 100"
                    />
                  )}
                </div>
              )}



              {loading && (
                <div className="flex items-center gap-2 text-indigo-500 animate-pulse">
                  <div className="w-1.5 h-1.5 bg-current rounded-full" />
                  <span className="text-[9px] font-black uppercase tracking-widest">Sincronizando Metadados...</span>
                </div>
              )}
            </div>
          )}

          {definition?.id === 'step_up_challenge_trigger' && (
            <div className="space-y-6 pt-8 border-t border-slate-100 animate-in slide-in-from-bottom-4 duration-500">
              <div className="flex items-center justify-between">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.3em] flex items-center gap-2">
                  <ShieldAlert size={14} className="text-rose-600" /> Step-Up Challenge
                </label>
                <span className="text-[8px] bg-rose-50 text-rose-600 px-2 py-1 rounded-lg font-black uppercase tracking-widest border border-rose-100">OTP Event</span>
              </div>

              <div className="bg-rose-50/70 border border-rose-100 rounded-2xl p-5 space-y-4">
                <div>
                  <label className="text-[10px] font-black text-rose-700 uppercase tracking-widest">Provider Filter</label>
                  <input
                    value={syncedNode.data.config?.provider || '*'}
                    onChange={(e) => onUpdate(syncedNode.id, {
                      ...syncedNode.data,
                      config: { ...(syncedNode.data.config || {}), provider: e.target.value || '*' }
                    })}
                    placeholder="z_api_whatsapp or *"
                    className="w-full mt-2 bg-white border border-rose-100 rounded-xl py-3 px-4 text-xs font-mono font-bold outline-none focus:border-rose-400"
                  />
                </div>
                <div className="bg-white/70 rounded-xl p-4 border border-rose-100">
                  <p className="text-[10px] text-slate-500 font-bold leading-relaxed">
                    Use um nó HTTP para chamar seu webhook e enviar o código com a variável mágica <code className="font-mono bg-slate-100 px-1 rounded">{'{{otp.code}}'}</code>.
                  </p>
                  <div className="flex flex-wrap gap-2 mt-3">
                    {['{{otp.code}}', '{{otp.provider}}', '{{user.identifier}}', '{{challenge.id}}'].map(v => (
                      <button key={v} onClick={() => navigator.clipboard.writeText(v)} className="text-[9px] font-mono font-bold bg-slate-900 text-white px-2 py-1 rounded-lg hover:bg-rose-700 transition-colors">
                        {v}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            </div>
          )}

          {definition?.id === 'webhook_trigger' && (
            <div className="space-y-8 pt-8 border-t border-slate-100 animate-in slide-in-from-bottom-4 duration-500">
              <div className="flex items-center justify-between">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.3em] flex items-center gap-2">
                  <Globe size={14} className="text-amber-500" /> Gateway Webhook
                </label>
                <span className="text-[8px] bg-amber-50 text-amber-600 px-2 py-1 rounded-lg font-black uppercase tracking-widest border border-amber-100">Public Entry Point</span>
              </div>

              <div className="space-y-4">
                <label className="text-[10px] font-black text-slate-600 uppercase tracking-widest">URL Path Slug</label>
                <div className="relative group/input">
                  <div className="absolute left-5 top-1/2 -translate-y-1/2 text-slate-300 font-mono text-[10px] pointer-events-none group-focus-within/input:text-amber-500 transition-colors">/webhook/</div>
                  <input
                    type="text"
                    className="w-full bg-slate-50 border-2 border-slate-100 rounded-2xl pl-24 pr-8 py-5 text-sm font-black text-slate-800 focus:border-amber-500 focus:bg-white transition-all outline-none"
                    placeholder="stripe-callback"
                    value={webhookPath}
                    onChange={(e) => setWebhookPath(e.target.value.toLowerCase().replace(/[^a-z0-9-_]/g, '-'))}
                  />
                </div>
                <div className="flex items-center justify-between gap-4">
                  <p className="text-[9px] text-slate-400 font-bold italic truncate">
                    Endpoint final: {(() => {
                      const activeBranch = localStorage.getItem('cascata_env') || 'live';
                      const isBranch = activeBranch && activeBranch !== 'live';
                      return projectData?.custom_domain
                        ? `https://${projectData.custom_domain}/webhook${isBranch ? `/branch/${activeBranch}` : ''}/${webhookPath || '...'}`
                        : `https://${projectId}.unibloom.com.br/webhook${isBranch ? `/branch/${activeBranch}` : ''}/${webhookPath || '...'}`;
                    })()}
                  </p>
                  <button
                    onClick={() => {
                      const activeBranch = localStorage.getItem('cascata_env') || 'live';
                      const isBranch = activeBranch && activeBranch !== 'live';
                      const url = projectData?.custom_domain
                        ? `https://${projectData.custom_domain}/webhook${isBranch ? `/branch/${activeBranch}` : ''}/${webhookPath}`
                        : `https://${projectId}.unibloom.com.br/webhook${isBranch ? `/branch/${activeBranch}` : ''}/${webhookPath}`;

                      let curl = `curl -X ${webhookMethod === 'ANY' ? 'POST' : webhookMethod} "${url}"`;
                      if (hasPolicy('bearer')) {
                        const ref = getPolicyConfig('bearer').vault_ref || 'YOUR_TOKEN';
                        curl += ` \\\n  -H "Authorization: Bearer ${ref}"`;
                      }
                      if (hasPolicy('hmac_sha256')) {
                        curl += ` \\\n  -H "X-Cascata-Signature: YOUR_HMAC_SIGNATURE"`;
                      }
                      if (hasPolicy('basic_auth')) {
                        curl += ` \\\n  -H "Authorization: Basic YOUR_BASE64_CREDENTIALS"`;
                      }
                      if (hasPolicy('anonymous')) {
                        curl += ` \\\n  -H "apikey: YOUR_ANON_KEY"`;
                      }
                      if (hasPolicy('api_key')) {
                        curl += ` \\\n  -H "apikey: sk_live_YOUR_API_KEY"`;
                      }
                      curl += ` \\\n  -H "Content-Type: application/json"`;
                      if (webhookMethod !== 'GET') {
                        curl += ` \\\n  -d '{"event": "test"}'`;
                      }

                      navigator.clipboard.writeText(curl);
                      setCopiedCurl(true);
                      setTimeout(() => setCopiedCurl(false), 2000);
                    }}
                    title="Copiar comando cURL"
                    className={`flex items-center gap-2 px-3 py-1.5 rounded-lg transition-all group/copy border ${copiedCurl ? 'bg-emerald-50 border-emerald-200 text-emerald-600' : 'bg-slate-50 border-slate-100 text-slate-400 hover:text-indigo-600 hover:bg-white'}`}
                  >
                    {copiedCurl ? <Check size={10} /> : <Copy size={10} className="group-hover/copy:scale-110 transition-transform" />}
                    <span className="text-[8px] font-black uppercase tracking-widest">{copiedCurl ? 'Copied!' : 'Copy cURL'}</span>
                  </button>
                </div>
              </div>

              <div className="space-y-4">
                <label className="text-[10px] font-black text-slate-600 uppercase tracking-widest">HTTP Method</label>
                <div className="flex flex-wrap gap-2">
                  {['POST', 'GET', 'PUT', 'DELETE', 'ANY'].map(m => (
                    <button
                      key={m}
                      onClick={() => setWebhookMethod(m)}
                      className={`px-5 py-3 rounded-2xl text-[10px] font-black transition-all border-2 ${webhookMethod === m ? 'bg-amber-500 border-amber-500 text-white shadow-lg shadow-amber-100' : 'bg-slate-50 border-slate-100 text-slate-400 hover:border-slate-200'}`}
                    >
                      {m}
                    </button>
                  ))}
                </div>
              </div>

              {/* ═══ AUTH POLICY BUILDER ═══ */}
              <div className="space-y-5">
                <div className="flex items-center justify-between">
                  <label className="text-[10px] font-black text-slate-600 uppercase tracking-widest">Barreiras de Autenticação</label>
                  <span className="text-[8px] bg-amber-100 text-amber-700 px-2 py-0.5 rounded font-black uppercase">{authPolicies.length === 0 ? 'Livre (sem proteção)' : `${authPolicies.length} barreira${authPolicies.length > 1 ? 's' : ''} ativa${authPolicies.length > 1 ? 's' : ''}`}</span>
                </div>

                <div className="grid grid-cols-3 gap-3">
                  {[
                    { id: 'none', label: 'Livre', desc: 'Sem proteção', icon: Globe, color: 'slate' },
                    { id: 'anonymous', label: 'App Client', desc: 'Requer anon_key', icon: Radio, color: 'blue' },
                    { id: 'api_key', label: 'API Key', desc: 'Requer sk_live_*', icon: Key, color: 'emerald' },
                    { id: 'identity', label: 'Identity', desc: 'Requer JWT', icon: ShieldCheck, color: 'violet' },
                    { id: 'basic_auth', label: 'Basic Auth', desc: 'Client ID/Secret', icon: Lock, color: 'sky' },
                    { id: 'bearer', label: 'Bearer Token', desc: 'Token fixo/Vault', icon: Bot, color: 'amber' },
                    { id: 'hmac_sha256', label: 'HMAC-SHA256', desc: 'Assinatura', icon: Lock, color: 'rose' },
                  ].map(method => {
                    const isActive = method.id === 'none' ? authPolicies.length === 0 : hasPolicy(method.id);
                    return (
                      <button
                        key={method.id}
                        onClick={() => togglePolicy(method.id)}
                        className={`relative flex flex-col items-center gap-1.5 p-4 rounded-2xl border-2 transition-all duration-300 ${isActive
                          ? `bg-${method.color}-50 border-${method.color}-400 text-${method.color}-600 shadow-lg scale-[1.03]`
                          : 'bg-slate-50 border-slate-100 text-slate-400 hover:border-slate-200 hover:bg-white'
                          }`}
                      >
                        <method.icon size={20} />
                        <span className="text-[8px] font-black uppercase tracking-widest text-center leading-tight">{method.label}</span>
                        <span className="text-[7px] font-medium opacity-60 text-center">{method.desc}</span>
                        {isActive && method.id !== 'none' && <div className="absolute top-1.5 right-1.5 w-2.5 h-2.5 bg-emerald-500 rounded-full border-2 border-white shadow" />}
                      </button>
                    );
                  })}
                </div>

                {/* ─── SUB-PANELS PER POLICY ─── */}
                {authPolicies.length > 0 && (
                  <div className="space-y-4 pt-2">
                    {/* ANONYMOUS: App Client Picker */}
                    {hasPolicy('anonymous') && (
                      <div className="p-5 bg-blue-50/50 border-2 border-blue-100 rounded-2xl space-y-3 animate-in zoom-in-95 duration-300">
                        <div className="flex items-center justify-between">
                          <label className="text-[10px] font-black text-blue-700 uppercase tracking-widest flex items-center gap-2">
                            <Radio size={14} /> App Clients Autorizados
                          </label>
                          <label className="flex items-center gap-2 cursor-pointer">
                            <input
                              type="checkbox"
                              checked={getPolicyConfig('anonymous').allow_project_key ?? true}
                              onChange={(e) => updatePolicyConfig('anonymous', { allow_project_key: e.target.checked })}
                              className="w-3.5 h-3.5 rounded accent-blue-600"
                            />
                            <span className="text-[8px] font-bold text-blue-500 uppercase">Permitir chave do projeto</span>
                          </label>
                        </div>
                        {availableAppClients.length > 0 ? (
                          <div className="grid grid-cols-2 gap-2">
                            {availableAppClients.map((ac: any) => {
                              const selected = (getPolicyConfig('anonymous').app_client_ids || []).includes(ac.id);
                              return (
                                <button
                                  key={ac.id}
                                  onClick={() => {
                                    const current = getPolicyConfig('anonymous').app_client_ids || [];
                                    const next = selected ? current.filter((x: string) => x !== ac.id) : [...current, ac.id];
                                    updatePolicyConfig('anonymous', { app_client_ids: next });
                                  }}
                                  className={`flex items-center gap-3 p-3 rounded-xl border-2 text-left transition-all ${selected ? 'bg-blue-100 border-blue-400 text-blue-700' : 'bg-white border-slate-100 text-slate-500 hover:border-blue-200'}`}
                                >
                                  <div className={`w-8 h-8 rounded-lg flex items-center justify-center text-[10px] font-black ${selected ? 'bg-blue-500 text-white' : 'bg-slate-100 text-slate-400'}`}>
                                    {ac.name?.[0]?.toUpperCase() || '?'}
                                  </div>
                                  <div>
                                    <span className="text-[10px] font-black block">{ac.name}</span>
                                    <span className="text-[8px] opacity-60">{ac.site_url || 'Sem domínio'}</span>
                                  </div>
                                  {selected && <Check size={14} className="ml-auto text-blue-600" />}
                                </button>
                              );
                            })}
                          </div>
                        ) : (
                          <p className="text-[9px] text-blue-400 italic font-bold">Nenhum App Client cadastrado neste projeto.</p>
                        )}
                      </div>
                    )}

                    {/* API KEY: Key Group Picker */}
                    {hasPolicy('api_key') && (
                      <div className="p-5 bg-emerald-50/50 border-2 border-emerald-100 rounded-2xl space-y-3 animate-in zoom-in-95 duration-300">
                        <label className="text-[10px] font-black text-emerald-700 uppercase tracking-widest flex items-center gap-2">
                          <Key size={14} /> Key Groups Autorizados
                        </label>
                        {availableKeyGroups.length > 0 ? (
                          <div className="grid grid-cols-2 gap-2">
                            {availableKeyGroups.map((g: any) => {
                              const selected = (getPolicyConfig('api_key').group_ids || []).includes(g.id);
                              return (
                                <button
                                  key={g.id}
                                  onClick={() => {
                                    const current = getPolicyConfig('api_key').group_ids || [];
                                    const next = selected ? current.filter((x: string) => x !== g.id) : [...current, g.id];
                                    updatePolicyConfig('api_key', { group_ids: next });
                                  }}
                                  className={`flex items-center gap-3 p-3 rounded-xl border-2 text-left transition-all ${selected ? 'bg-emerald-100 border-emerald-400 text-emerald-700' : 'bg-white border-slate-100 text-slate-500 hover:border-emerald-200'}`}
                                >
                                  <div className={`w-8 h-8 rounded-lg flex items-center justify-center text-[10px] font-black ${selected ? 'bg-emerald-500 text-white' : 'bg-slate-100 text-slate-400'}`}>
                                    {g.name?.[0]?.toUpperCase() || '?'}
                                  </div>
                                  <div>
                                    <span className="text-[10px] font-black block">{g.name}</span>
                                    <span className="text-[8px] opacity-60">{g.rate_limit}/s • Burst {g.burst_limit}</span>
                                  </div>
                                  {selected && <Check size={14} className="ml-auto text-emerald-600" />}
                                </button>
                              );
                            })}
                          </div>
                        ) : (
                          <p className="text-[9px] text-emerald-400 italic font-bold">Nenhum Key Group cadastrado. Crie em Traffic Guard → Access Credentials.</p>
                        )}
                      </div>
                    )}

                    {/* IDENTITY: Role Picker */}
                    {hasPolicy('identity') && (
                      <div className="p-5 bg-violet-50/50 border-2 border-violet-100 rounded-2xl space-y-3 animate-in zoom-in-95 duration-300">
                        <label className="text-[10px] font-black text-violet-700 uppercase tracking-widest flex items-center gap-2">
                          <UserCheck size={14} /> Nível Mínimo de Identidade
                        </label>
                        <div className="flex gap-2">
                          {[
                            { id: 'authenticated', label: 'Autenticado', desc: 'Qualquer usuário logado' },
                            { id: 'admin', label: 'Admin', desc: 'Somente administradores' },
                            { id: 'service', label: 'Service', desc: 'Somente service_role' },
                          ].map(role => (
                            <button
                              key={role.id}
                              onClick={() => updatePolicyConfig('identity', { min_role: role.id })}
                              className={`flex-1 p-3 rounded-xl border-2 text-center transition-all ${getPolicyConfig('identity').min_role === role.id ? 'bg-violet-100 border-violet-400 text-violet-700' : 'bg-white border-slate-100 text-slate-400 hover:border-violet-200'}`}
                            >
                              <span className="text-[9px] font-black uppercase block">{role.label}</span>
                              <span className="text-[7px] opacity-60">{role.desc}</span>
                            </button>
                          ))}
                        </div>
                      </div>
                    )}

                    {/* BEARER / HMAC / RSA / BASIC AUTH: Vault Picker */}
                    {(hasPolicy('bearer') || hasPolicy('hmac_sha256') || hasPolicy('rsa_signature') || hasPolicy('basic_auth')) && (
                      <div className={`p-5 border-2 rounded-2xl space-y-3 animate-in zoom-in-95 duration-300 ${hasPolicy('basic_auth') ? 'bg-sky-50/50 border-sky-100' : 'bg-amber-50/50 border-amber-100'}`}>
                        <div className="flex items-center justify-between">
                          <label className={`text-[10px] font-black uppercase tracking-widest flex items-center gap-2 ${hasPolicy('basic_auth') ? 'text-sky-700' : 'text-amber-700'}`}>
                            <Lock size={14} />
                            {hasPolicy('bearer') ? 'Bearer Token' : hasPolicy('rsa_signature') ? 'Chave Pública (PEM)' : hasPolicy('basic_auth') ? 'Basic Auth Secret' : 'Secret Key (HMAC)'}
                          </label>
                          <button
                            onClick={() => {
                              const targetMethod = hasPolicy('bearer') ? 'bearer' : hasPolicy('rsa_signature') ? 'rsa_signature' : hasPolicy('basic_auth') ? 'basic_auth' : 'hmac_sha256';
                              setPickerCallback(() => (val: string) => updatePolicyConfig(targetMethod, { vault_ref: `{{${val}}}` }));
                              setIsPickerOpen(true);
                            }}
                            className="text-[9px] font-black text-indigo-500 uppercase hover:underline flex items-center gap-1"
                          >
                            <ShieldCheck size={12} /> Usar Vault
                          </button>
                        </div>
                        {['bearer', 'hmac_sha256', 'rsa_signature', 'basic_auth'].filter(m => hasPolicy(m)).map(m => (
                          <div key={m} className="space-y-1">
                            {authPolicies.filter(p => ['bearer', 'hmac_sha256', 'rsa_signature', 'basic_auth'].includes(p.method)).length > 1 && (
                              <span className={`text-[8px] font-black uppercase ${m === 'basic_auth' ? 'text-sky-500' : 'text-amber-500'}`}>{m === 'bearer' ? 'Bearer' : m === 'rsa_signature' ? 'RSA' : m === 'basic_auth' ? 'Basic Auth' : 'HMAC'}</span>
                            )}
                            <input
                              type={m === 'basic_auth' ? 'text' : 'password'}
                              className={`w-full bg-white border-2 rounded-xl px-6 py-4 text-sm font-black text-slate-800 transition-all outline-none ${m === 'basic_auth' ? 'border-sky-100 focus:border-sky-500' : 'border-amber-100 focus:border-amber-500'}`}
                              placeholder={m === 'rsa_signature' ? '-----BEGIN PUBLIC KEY----- ...' : m === 'bearer' ? 'Token de autorização...' : m === 'basic_auth' ? 'Referência ao Basic Auth do Vault...' : 'Chave secreta para validação...'}
                              value={getPolicyConfig(m).vault_ref || ''}
                              onChange={(e) => updatePolicyConfig(m, { vault_ref: e.target.value })}
                            />
                          </div>
                        ))}
                        <p className={`text-[8px] italic ${hasPolicy('basic_auth') ? 'text-sky-500' : 'text-amber-500'}`}>Use referências Vault como {'{'}{'{'} $vault.SEU_SEGREDO.value {'}'}{'}'} para resolução dinâmica.</p>
                      </div>
                    )}
                  </div>
                )}
              </div>

              <div className="space-y-4 pt-4 border-t border-slate-50 animate-in fade-in duration-700">
                <div className="flex items-center justify-between">
                  <label className="text-[10px] font-black text-slate-600 uppercase tracking-widest flex items-center gap-2">
                    <Zap size={14} className="text-amber-500" /> Resposta Assíncrona (Acknowledge)
                  </label>
                  <span className="text-[8px] bg-slate-100 text-slate-400 px-2 py-0.5 rounded font-bold uppercase">Async Only</span>
                </div>
                <div className="relative group/input">
                  <textarea
                    className="w-full bg-slate-50 border-2 border-slate-100 rounded-2xl px-6 py-4 text-[10px] font-mono text-slate-700 focus:border-amber-500 focus:bg-white transition-all outline-none resize-none h-20"
                    placeholder={`{"status": "received", "success": true}`}
                    value={webhookAsyncResponse}
                    onChange={(e) => setWebhookAsyncResponse(e.target.value)}
                  />
                </div>
                <p className="text-[9px] text-slate-400 font-bold italic opacity-60">Esta resposta será enviada IMEDIATAMENTE se a automação for 'Worker-Lane' (processamento em segundo plano).</p>
              </div>

              <div className="space-y-4 pt-8 border-t border-slate-100 animate-in slide-in-from-bottom-4 duration-700">
                <div className="flex items-center justify-between">
                  <label className="text-[10px] font-black text-slate-600 uppercase tracking-widest flex items-center gap-2">
                    <Braces size={14} className="text-indigo-500" /> Payload Transformation (Entrada)
                  </label>
                  <div className="flex bg-slate-100 p-1 rounded-lg">
                    <button
                      onClick={() => setMappingMode('no-code')}
                      className={`px-3 py-1 rounded-md text-[8px] font-black uppercase transition-all ${mappingMode === 'no-code' ? 'bg-white shadow-sm text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                    >
                      No-Code
                    </button>
                    <button
                      onClick={() => {
                        if (mappingMode === 'no-code') {
                          const newMap = mappingRows.reduce((acc, r) => {
                            if (r.column) acc[r.column] = r.value;
                            return acc;
                          }, {} as Record<string, any>);
                          setMapperValue(JSON.stringify(newMap, null, 2));
                        }
                        setMappingMode('code');
                      }}
                      className={`px-3 py-1 rounded-md text-[8px] font-black uppercase transition-all ${mappingMode === 'code' ? 'bg-white shadow-sm text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                    >
                      Code
                    </button>
                  </div>
                </div>

                <p className="text-[9px] text-slate-400 font-bold italic opacity-60">Normalize os dados recebidos antes de iniciar o fluxo. Use {`{{$trigger.raw}}`} para acessar o payload bruto.</p>

                {detectedPayload && (
                  <div className="p-6 bg-slate-900 rounded-[2rem] border border-slate-800 space-y-4 animate-in zoom-in-95 duration-500 relative overflow-hidden group/json">
                    <div className="absolute top-0 right-0 p-4 opacity-10 group-hover/json:opacity-20 transition-opacity">
                      <Braces size={80} className="text-indigo-500" />
                    </div>

                    <div className="flex items-center justify-between relative z-10">
                      <div className="flex items-center gap-2">
                        <div className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
                        <span className="text-[9px] font-black text-indigo-400 uppercase tracking-widest">Payload Detectado (Tree View)</span>
                      </div>
                      <span className="text-[8px] font-bold text-slate-500 uppercase tracking-widest">Clique nas linhas para mapear</span>
                    </div>

                    <div className="max-h-60 overflow-y-auto custom-scrollbar-dark pr-2 relative z-10">
                      <JSONVariablePicker
                        data={detectedPayload}
                        onSelect={(fullPath, leafName) => {
                          if (!mappingRows.some(r => typeof r.value === 'string' && r.value.includes(fullPath))) {
                            setMappingRows([...mappingRows, { column: leafName, value: `{{$trigger.${fullPath}}}` }]);
                          }
                        }}
                      />
                    </div>
                  </div>
                )}

                {(!detectedPayload && detectedFields.length > 0) && (
                  <div className="p-4 bg-indigo-50/30 rounded-2xl border border-indigo-100/50 space-y-2 animate-in zoom-in-95 duration-500">
                    <div className="flex items-center gap-2 mb-2">
                      <Search size={12} className="text-indigo-400" />
                      <span className="text-[9px] font-black text-indigo-500 uppercase tracking-widest">Campos Detectados</span>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {detectedFields.map(field => (
                        <button
                          key={field}
                          onClick={() => {
                            const parts = field.split('.');
                            const leafName = parts[parts.length - 1];
                            if (!mappingRows.some(r => typeof r.value === 'string' && r.value.includes(field))) {
                              setMappingRows([...mappingRows, { column: leafName, value: `{{$trigger.${field}}}` }]);
                            }
                          }}
                          className="px-3 py-1.5 bg-white border border-indigo-100 rounded-lg text-[10px] font-bold text-indigo-600 hover:bg-indigo-600 hover:text-white hover:scale-105 transition-all shadow-sm flex items-center gap-2 group"
                        >
                          <Plus size={10} className="opacity-40 group-hover:opacity-100" /> {field}
                        </button>
                      ))}
                    </div>
                  </div>
                )}

                {mappingMode === 'no-code' ? (
                  <div className="space-y-3">
                    {mappingRows.map((row, idx) => (
                      <div key={idx} className="flex gap-2 items-center animate-in fade-in slide-in-from-left-2">
                        <input
                          type="text"
                          className="flex-1 bg-slate-50 border-2 border-slate-100 rounded-xl px-3 py-3 text-[10px] font-bold outline-none focus:border-indigo-400"
                          placeholder="Chave interna..."
                          value={row.column}
                          onChange={(e) => {
                            const newRows = [...mappingRows];
                            newRows[idx].column = e.target.value;
                            setMappingRows(newRows);
                          }}
                        />
                        <ArrowRight size={12} className="text-slate-300 flex-shrink-0" />
                        <div className="flex-[1.5] relative group/input">
                          <input
                            type="text"
                            className="w-full bg-slate-50 border-2 border-slate-100 rounded-xl px-3 py-3 pr-16 text-[10px] font-bold outline-none focus:border-indigo-400"
                            placeholder="Valor ou {{$trigger.raw.xxx}}..."
                            value={row.value}
                            onChange={(e) => {
                              const newRows = [...mappingRows];
                              newRows[idx].value = e.target.value;
                              setMappingRows(newRows);
                            }}
                          />
                          <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-1 opacity-0 group-hover/input:opacity-100 transition-all">
                            {typeof row.value === 'string' && row.value.includes('}}') && (
                              <button
                                onClick={() => {
                                  setPipePickerCallback(() => (pipe: string) => {
                                    const newRows = [...mappingRows];
                                    const val = newRows[idx].value.trim();
                                    if (val.endsWith('}}')) {
                                      newRows[idx].value = val.replace(/\}\}$ /g, '').replace(/\}\}$/g, ` | ${pipe}}}`);
                                      setMappingRows(newRows);
                                    }
                                  });
                                  setIsPipePickerOpen(true);
                                }}
                                className="text-slate-300 hover:text-emerald-500 p-1"
                                title="Transformar dado"
                              >
                                <Wand2 size={12} />
                              </button>
                            )}
                            <button
                              onClick={() => {
                                setPickerCallback(() => (val: string) => {
                                  const newRows = [...mappingRows];
                                  newRows[idx].value = `{{${val}}}`;
                                  setMappingRows(newRows);
                                });
                                setIsPickerOpen(true);
                              }}
                              className="text-slate-300 hover:text-indigo-500 p-1"
                              title="Inserir variável"
                            >
                              <Plus size={14} />
                            </button>
                          </div>
                        </div>
                        <button
                          onClick={() => setMappingRows(mappingRows.filter((_, i) => i !== idx))}
                          className="w-8 h-8 flex items-center justify-center text-rose-300 hover:text-rose-500 transition-colors"
                        >
                          <Trash2 size={14} />
                        </button>
                      </div>
                    ))}
                    <button
                      onClick={() => setMappingRows([...mappingRows, { column: '', value: '' }])}
                      className="w-full py-2 border-2 border-dashed border-slate-200 rounded-xl text-[9px] font-black text-slate-400 hover:border-indigo-300 hover:text-indigo-500 transition-all uppercase tracking-widest flex items-center justify-center gap-2"
                    >
                      <Plus size={12} /> Add Mapeamento
                    </button>
                  </div>
                ) : (
                  <VariableMapper
                    label=""
                    value={mapperValue}
                    onChange={setMapperValue}
                    availableNodes={availableNodes}
                    expectedType="json"
                    projectId={projectId}
                    testLogs={testLogs}
                  />
                )}
              </div>
            </div>
          )}

          {definition?.id === 'condition_if' && (
            <div className="space-y-6 pt-6 border-t border-slate-100">
              <div className="flex items-center justify-between">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Rotas Lógicas (If / Else If)</label>
                <span className="text-[8px] bg-purple-100 text-purple-600 px-2 py-0.5 rounded font-bold uppercase">Enterprise Logic</span>
              </div>

              <div className="space-y-4">
                {logicRoutes.map((route, rIdx) => (
                  <div key={rIdx} className="p-5 bg-slate-50 rounded-2xl border border-slate-100 space-y-4 animate-in slide-in-from-bottom-2">
                    <div className="flex items-center justify-between">
                      <span className="text-[9px] font-black text-indigo-500 uppercase tracking-widest">Rota {rIdx} (Port: route_{rIdx})</span>
                      <button onClick={() => setLogicRoutes(logicRoutes.filter((_, i) => i !== rIdx))} className="text-rose-400 hover:text-rose-600 transition-colors">
                        <Trash2 size={14} />
                      </button>
                    </div>

                    <div className="flex items-center gap-2">
                      <input
                        type="text"
                        placeholder="Alias da Rota (ex: Sucesso)..."
                        className="flex-1 bg-white border border-slate-200 rounded-lg px-3 py-2 text-[10px] font-black text-indigo-600 outline-none focus:border-indigo-400"
                        value={route.label || ''}
                        onChange={(e) => {
                          const next = [...logicRoutes];
                          next[rIdx].label = e.target.value;
                          setLogicRoutes(next);
                        }}
                      />
                    </div>

                    {route.conditions.map((cond: any, cIdx: number) => (
                      <div key={cIdx} className="flex gap-2">
                        <div className="flex-1 relative group/input">
                          <input
                            type="text"
                            placeholder="Campo..."
                            className="w-full bg-white border border-slate-200 rounded-lg px-3 py-2 pr-7 text-[10px] font-bold outline-none focus:border-indigo-400"
                            value={cond.column}
                            onChange={(e) => {
                              const next = [...logicRoutes];
                              next[rIdx].conditions[cIdx].column = e.target.value;
                              setLogicRoutes(next);
                            }}
                          />
                          <button
                            onClick={() => {
                              setPickerCallback(() => (val: string) => {
                                const next = [...logicRoutes];
                                next[rIdx].conditions[cIdx].column = `{{${val}}}`;
                                setLogicRoutes(next);
                              });
                              setIsPickerOpen(true);
                            }}
                            className="absolute right-1.5 top-1/2 -translate-y-1/2 text-slate-300 hover:text-indigo-500 opacity-0 group-hover/input:opacity-100 transition-all"
                          >
                            <Search size={12} />
                          </button>
                        </div>
                        <select
                          className="w-16 bg-white border border-slate-200 rounded-lg px-1 py-2 text-[10px] font-bold outline-none"
                          value={cond.operator}
                          onChange={(e) => {
                            const next = [...logicRoutes];
                            next[rIdx].conditions[cIdx].operator = e.target.value;
                            setLogicRoutes(next);
                          }}
                        >
                          <option value="==">==</option>
                          <option value="!=">!=</option>
                          <option value=">">&gt;</option>
                          <option value="<">&lt;</option>
                          <option value="in">in</option>
                          <option value="not_in">not in</option>
                          <option value="contains">contém</option>
                          <option value="not_contains">!contém</option>
                        </select>
                        <div className="flex-1 relative group/input">
                          <input
                            type="text"
                            placeholder="Valor..."
                            className="w-full bg-white border border-slate-200 rounded-lg px-3 py-2 pr-7 text-[10px] font-bold outline-none focus:border-indigo-400"
                            value={cond.value}
                            onChange={(e) => {
                              const next = [...logicRoutes];
                              next[rIdx].conditions[cIdx].value = e.target.value;
                              setLogicRoutes(next);
                            }}
                          />
                          <button
                            onClick={() => {
                              setPickerCallback(() => (val: string) => {
                                const next = [...logicRoutes];
                                next[rIdx].conditions[cIdx].value = `{{${val}}}`;
                                setLogicRoutes(next);
                              });
                              setIsPickerOpen(true);
                            }}
                            className="absolute right-1.5 top-1/2 -translate-y-1/2 text-slate-300 hover:text-indigo-500 opacity-0 group-hover/input:opacity-100 transition-all"
                          >
                            <Plus size={12} />
                          </button>
                        </div>
                        <button onClick={() => {
                          const next = [...logicRoutes];
                          next[rIdx].conditions = next[rIdx].conditions.filter((_: any, i: number) => i !== cIdx);
                          setLogicRoutes(next);
                        }} className="text-slate-300 hover:text-rose-400">
                          <X size={12} />
                        </button>
                      </div>
                    ))}
                    <button
                      onClick={() => {
                        const next = [...logicRoutes];
                        next[rIdx].conditions.push({ column: '', operator: '==', value: '' });
                        setLogicRoutes(next);
                      }}
                      className="text-[8px] font-black text-indigo-400 uppercase tracking-widest hover:text-indigo-600 transition-colors"
                    >
                      + Add Condição (AND)
                    </button>
                  </div>
                ))}
                <button
                  onClick={() => setLogicRoutes([...logicRoutes, { conditions: [{ column: '', operator: '==', value: '' }] }])}
                  className="w-full py-4 border-2 border-dashed border-indigo-200 rounded-2xl text-[9px] font-black text-indigo-400 hover:bg-indigo-50 transition-all uppercase tracking-widest flex items-center justify-center gap-2"
                >
                  <Plus size={14} /> Nova Rota Lógica
                </button>
              </div>
            </div>
          )}

          {definition?.id === 'http_request' && (
            <div className="space-y-6 pt-6 border-t border-slate-100">
              <div className="flex items-center justify-between">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Configuração HTTP</label>
                <span className="text-[8px] bg-blue-100 text-blue-600 px-2 py-0.5 rounded font-bold uppercase">REST API</span>
              </div>

              <HTTPNodeSimple
                config={node.data.config || {
                  url: '',
                  method: 'GET',
                  bodyType: 'none',
                  headers: {},
                  queryParams: {},
                  authType: 'none',
                  timeout: 30000,
                  retryCount: 0,
                  followRedirects: true,
                  validateSSL: true,
                  responseFormat: 'auto'
                }}
                onChange={(config) => {
                  onUpdate(syncedNode.id, {
                    ...syncedNode.data,
                    config,
                    configSummary: config.url ? `${config.method} ${config.url}` : 'Aguardando configuração'
                  });
                }}
                vaultSecrets={[]}
                projectId={projectId || ''}
                availableNodes={availableNodes}
                testLogs={testLogs}
              />
            </div>
          )}

          {definition?.id === 'response_node' && (
            <div className="pt-4 border-t border-slate-100 space-y-4">
              <div className="flex items-center justify-between">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Tipo de Resposta</label>
                <select
                  value={responseType}
                  onChange={(e) => {
                    setResponseType(e.target.value);
                    onUpdate(syncedNode.id, {
                      ...syncedNode.data,
                      config: {
                        ...syncedNode.data.config,
                        responseType: e.target.value
                      }
                    });
                  }}
                  className="text-[10px] font-bold bg-slate-50 border-2 border-slate-100 rounded-lg px-3 py-2 outline-none focus:border-indigo-400"
                >
                  <option value="json">JSON</option>
                  <option value="text">Texto</option>
                  <option value="html">HTML</option>
                  <option value="xml">XML</option>
                </select>
              </div>
            </div>
          )}

          {(!isTrigger && definition?.id !== 'condition_if' && definition?.id !== 'http_request') && (
            <div className="pt-4 border-t border-slate-100 space-y-4">
              <div className="flex items-center justify-between">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Mapping & Payload</label>
                <div className="flex bg-slate-100 p-1 rounded-lg gap-1">
                  <button
                    onClick={() => setMappingMode('no-code')}
                    className={`px-3 py-1 rounded-md text-[8px] font-black uppercase transition-all ${mappingMode === 'no-code' ? 'bg-white shadow-sm text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                  >
                    No-Code
                  </button>
                  <button
                    onClick={() => setMappingMode('code')}
                    className={`px-3 py-1 rounded-md text-[8px] font-black uppercase transition-all ${mappingMode === 'code' ? 'bg-white shadow-sm text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                  >
                    Code
                  </button>
                </div>
              </div>

              {mappingMode === 'no-code' ? (
                <div className="space-y-3">
                  {mappingRows.map((row, idx) => (
                    <div key={idx} className="flex gap-2 items-center animate-in fade-in slide-in-from-left-2">
                      <div className="flex-1 relative">
                        <input
                          list={`col-suggestions-${idx}`}
                          type="text"
                          className="w-full bg-slate-50 border-2 border-slate-100 rounded-xl px-3 py-3 text-[10px] font-bold outline-none focus:border-indigo-400"
                          placeholder="coluna ou chave..."
                          value={row.column}
                          onChange={(e) => {
                            const newRows = [...mappingRows];
                            newRows[idx].column = e.target.value;
                            setMappingRows(newRows);
                          }}
                        />
                        <datalist id={`col-suggestions-${idx}`}>
                          {columns.map(c => <option key={c.name} value={c.name} />)}
                        </datalist>
                      </div>
                      <ArrowRight size={12} className="text-slate-300 flex-shrink-0" />
                      <div className="flex-[1.5] relative group/input">
                        <input
                          type="text"
                          className="w-full bg-slate-50 border-2 border-slate-100 rounded-xl px-3 py-3 pr-16 text-[10px] font-bold outline-none focus:border-indigo-400"
                          placeholder="Valor ou {{variavel}}..."
                          value={row.value}
                          onChange={(e) => {
                            const newRows = [...mappingRows];
                            newRows[idx].value = e.target.value;
                            setMappingRows(newRows);
                          }}
                        />
                        <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-1 opacity-0 group-hover/input:opacity-100 transition-all">
                          {typeof row.value === 'string' && row.value.includes('}}') && (
                            <button
                              onClick={() => {
                                setPipePickerCallback(() => (pipe: string) => {
                                  const newRows = [...mappingRows];
                                  const val = newRows[idx].value.trim();
                                  if (val.endsWith('}}')) {
                                    newRows[idx].value = val.replace(/\}\}$ /g, '').replace(/\}\}$/g, ` | ${pipe}}}`);
                                    setMappingRows(newRows);
                                  }
                                });
                                setIsPipePickerOpen(true);
                              }}
                              className="text-slate-300 hover:text-emerald-500 p-1"
                              title="Transformar dado"
                            >
                              <Wand2 size={12} />
                            </button>
                          )}
                          <button
                            onClick={() => {
                              setPickerCallback(() => (val: string) => {
                                const newRows = [...mappingRows];
                                newRows[idx].value = `{{${val}}}`;
                                setMappingRows(newRows);
                              });
                              setIsPickerOpen(true);
                            }}
                            className="text-slate-300 hover:text-indigo-500 p-1"
                            title="Inserir variável"
                          >
                            <Plus size={14} />
                          </button>
                        </div>
                      </div>
                      <button
                        onClick={() => setMappingRows(mappingRows.filter((_, i) => i !== idx))}
                        className="w-8 h-8 flex items-center justify-center text-rose-300 hover:text-rose-500 transition-colors"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  ))}
                  <button
                    onClick={() => setMappingRows([...mappingRows, { column: '', value: '' }])}
                    className="w-full py-2 border-2 border-dashed border-slate-200 rounded-xl text-[9px] font-black text-slate-400 hover:border-indigo-300 hover:text-indigo-500 transition-all uppercase tracking-widest flex items-center justify-center gap-2"
                  >
                    <Plus size={12} /> Add Campo
                  </button>
                </div>
              ) : (
                <VariableMapper
                  label=""
                  value={mapperValue}
                  onChange={setMapperValue}
                  availableNodes={availableNodes}
                  expectedType={definition?.inputs[0] || 'any'}
                  projectId={projectId}
                  testLogs={testLogs}
                />
              )}
            </div>
          )}

        </div>

        <div className="bg-slate-900 rounded-[3rem] p-10 text-white relative overflow-hidden shadow-2xl border border-slate-800">
          <div className="absolute top-0 right-0 p-12 opacity-[0.03] rotate-45 scale-150">
            <BrainCircuit size={200} />
          </div>
          <div className="relative z-10">
            <h4 className="text-[11px] font-black uppercase tracking-[0.4em] text-indigo-400 mb-6">Nexus Engine Analysis</h4>
            <p className="text-sm font-medium text-slate-400 leading-relaxed mb-8">
              {isTrigger
                ? "Este gatilho síncrono irá suspender a operação original caso as condições de filtro sejam atendidas."
                : "Este nó utilizará o Token de Soberania para garantir Auto-Imunidade contra loops recursivos."}
            </p>
            <button className="w-full bg-white text-slate-900 py-5 rounded-2xl font-black text-[11px] uppercase tracking-widest shadow-xl hover:bg-slate-100 transition-all active:scale-95 flex items-center justify-center gap-3">
              <Code2 size={18} /> Editar Payload JSON
            </button>
          </div>
        </div>
      </div>

      <div className="p-12 bg-white border-t border-slate-50 flex gap-5">
        <button
          onClick={handleSave}
          className="flex-1 bg-slate-900 text-white font-black text-[12px] uppercase tracking-[0.3em] py-6 rounded-[2rem] shadow-[0_30px_60px_-10px_rgba(0,0,0,0.3)] hover:bg-black active:scale-95 transition-all flex items-center justify-center gap-4"
        >
          <Save size={20} /> Salvar Alterações
        </button>
        <button
          onClick={() => { onDelete(syncedNode.id); onClose(); }}
          className="w-20 h-20 bg-rose-50 text-rose-500 rounded-[2rem] hover:bg-rose-500 hover:text-white transition-all flex items-center justify-center shadow-lg hover:shadow-rose-100"
        >
          <Trash2 size={28} />
        </button>
      </div>

      <VariablePickerModal
        isOpen={isPickerOpen}
        onClose={() => setIsPickerOpen(false)}
        onSelect={(val) => {
          pickerCallback(val);
          setIsPickerOpen(false);
        }}
        availableNodes={availableNodes}
        projectId={projectId || ''}
        testLogs={testLogs}
      />

      <PipePickerModal
        isOpen={isPipePickerOpen}
        onClose={() => setIsPipePickerOpen(false)}
        onSelect={pipePickerCallback}
      />
    </div>
  );
};

// ============================================================================
// MAIN ARCHITECT
// ============================================================================

// Definidos FORA do componente para evitar TDZ e re-criação a cada render
const NODE_TYPES = {
  nexusNode: NexusNode,
} as const;

const EDGE_TYPES = {
  premiumEdge: PremiumEdge,
} as const;

interface NexusArchitectProps {
  projectId?: string;
  automation?: any;
  nodes?: any[];
  onSave?: (payload: any) => void;
  onBack?: () => void;
  onTestRun?: (automationId: string, payload?: any) => Promise<{ execution_id: string; status: string; duration_ms: number }>;
  onFetchStepLogs?: (executionId: string) => Promise<any[]>;
  onStartListening?: (automationId: string) => Promise<boolean>;
  onStopListening?: (automationId: string) => Promise<boolean>;
  onCheckExecution?: (automationId: string) => Promise<string | null>;
}

const NexusArchitectContent: React.FC<NexusArchitectProps> = ({
  projectId, automation, nodes: initialPropsNodes, onSave, onBack, onTestRun, onFetchStepLogs,
  onStartListening, onStopListening, onCheckExecution
}) => {

  const processInitialNodes = (rawNodes?: any[]): Node[] => {
    if (!rawNodes || rawNodes.length === 0) {
      return [];  // Canvas vazio — modal de seleção será aberto automaticamente
    }

    return rawNodes.map((rn) => {
      // Find the correct nodeId (library ID) from either field
      const libraryId = rn.node_id || rn.nodeId || (rn.type === 'trigger' ? 'webhook_trigger' : 'http_request');
      const definition = NODE_LIBRARY.find(d => d.id === libraryId);

      // Para nós de condição, calcular outputs dinamicamente baseado em config.routes
      let dynamicOutputs = definition?.outputs || [];
      if (libraryId === 'condition_if' && rn.config?.routes) {
        const routes = rn.config.routes as any[];
        if (routes.length > 0) {
          // N rotas + 1 else = N+1 outputs
          dynamicOutputs = [...routes.map(() => 'any'), 'any'];
        }
      }

      return {
        id: rn.id,
        type: 'nexusNode',
        position: { x: rn.x || 400, y: rn.y || 50 },
        data: {
          id: rn.id,
          nodeId: libraryId,
          label: rn.label || (definition?.label),
          type: rn.type,
          config: rn.config || {},
          outputs: dynamicOutputs,
          configSummary: rn.configSummary || (rn.config?.table ? `Table: ${rn.config.table}` : rn.config?.url ? `URL: ${rn.config.url}` : ''),
          onAddFromAntenna: (id: string, handleId: string) => {
            setActiveHandle({ nodeId: id, handleId });
            setIsLibraryOpen(true);
            setActiveEdgeId(null);
          }
        }
      };
    });
  };

  const processInitialEdges = (rawNodes?: any[]): Edge[] => {
    if (!rawNodes || rawNodes.length === 0) return [];
    if (Array.isArray(automation?.edges) && automation.edges.length > 0) {
      return automation.edges.map((edge: any) => {
        const sourceHandle = edge.sourceHandle || edge.source_handle;
        let edgeLabel: string | undefined;

        // Determinar label baseado no sourceHandle
        if (sourceHandle === 'true') {
          edgeLabel = 'DOES MATCH';
        } else if (sourceHandle === 'false' || sourceHandle === 'else') {
          edgeLabel = 'ELSE';
        } else if (sourceHandle?.startsWith('route_')) {
          // Buscar o label da rota no nó source
          const sourceNode = rawNodes?.find(n => n.id === edge.source);
          const routeIdx = parseInt(sourceHandle.replace('route_', '')) || 0;
          const routes = sourceNode?.config?.routes || [];
          edgeLabel = routes[routeIdx]?.label || `ROTA ${routeIdx}`;
        }

        return {
          id: edge.id || `e-${edge.source}-${edge.target}`,
          source: edge.source,
          target: edge.target,
          sourceHandle: sourceHandle || undefined,
          targetHandle: edge.targetHandle || edge.target_handle || undefined,
          type: 'premiumEdge',
          label: edgeLabel,
          data: { onAddNode: () => { } }
        };
      });
    }
    const edges: Edge[] = [];

    rawNodes.forEach(sourceNode => {
      if (sourceNode.next && Array.isArray(sourceNode.next)) {
        const isCondition = sourceNode.node_id === 'condition_if';
        const routes = sourceNode.config?.routes || [];
        const routeCount = routes.length;

        sourceNode.next.forEach((targetId: string, idx: number) => {
          const targetNode = rawNodes.find(n => n.id === targetId);
          if (targetNode) {
            // Inferir sourceHandle baseado no índice e tipo de nó
            let sourceHandle: string | undefined;
            let edgeLabel: string | undefined;

            if (isCondition) {
              // Se há N rotas configuradas, next tem N+1 itens: route_0..route_N-1, else
              if (routeCount > 0) {
                if (idx < routeCount) {
                  sourceHandle = `route_${idx}`;
                  edgeLabel = routes[idx]?.label || `ROTA ${idx}`;
                } else {
                  sourceHandle = 'else';
                  edgeLabel = 'ELSE';
                }
              } else {
                // Modo legado: primeira é 'true', segunda é 'false'
                sourceHandle = idx === 0 ? 'true' : (idx === 1 ? 'false' : `route_${idx}`);
                edgeLabel = idx === 0 ? 'DOES MATCH' : (idx === 1 ? 'ELSE' : `ROTA ${idx}`);
              }
            }

            edges.push({
              id: `e-${sourceNode.id}-${targetId}-${sourceHandle || 'next'}`,
              source: sourceNode.id,
              target: targetId,
              sourceHandle,
              type: 'premiumEdge',
              label: edgeLabel,
              data: { onAddNode: () => { } }
            });
          }
        });
      }
    });

    return edges;
  };

  const [nodes, setNodes, onNodesChange] = useNodesState(processInitialNodes(initialPropsNodes));
  const [edges, setEdges, onEdgesChange] = useEdgesState(processInitialEdges(initialPropsNodes));
  const [isLibraryOpen, setIsLibraryOpen] = useState(false);
  const [activeEdgeId, setActiveEdgeId] = useState<string | null>(null);
  const [activeHandle, setActiveHandle] = useState<{ nodeId: string, handleId: string } | null>(null);
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);
  const [automationName, setAutomationName] = useState(() => {
    if (automation?.name) return automation.name;
    const randomSuffix = Math.floor(100 + Math.random() * 900);
    return `Nova Orquestração ${randomSuffix}`;
  });
  const [isEditingName, setIsEditingName] = useState(false);
  const [executionMode, setExecutionMode] = useState<'linear' | 'parallel'>(automation?.execution_mode || 'linear');
  const [isTestRunning, setIsTestRunning] = useState(false);
  const [isListening, setIsListening] = useState(false);
  const [testLogs, setTestLogs] = useState<any[]>([]);
  const [currentExecutingNode, setCurrentExecutingNode] = useState<string | null>(null);
  const [isTelemetryMinimized, setIsTelemetryMinimized] = useState(false);

  const handleToggleListening = async () => {
    if (!automation?.id || !onStartListening || !onStopListening || !onFetchStepLogs) return;

    if (isListening) {
      await onStopListening(automation.id);
      setIsListening(false);
      return;
    }

    setIsListening(true);
    setTestLogs([]);
    setCurrentExecutingNode(null);

    const started = await onStartListening(automation.id);
    if (!started) {
      setIsListening(false);
      alert("Falha ao iniciar modo escuta. Verifique se o fluxo está ativo.");
    }
  };

  // Polling for real execution when in Listening Mode
  useEffect(() => {
    let interval: NodeJS.Timeout;
    if (isListening && onCheckExecution && !isTestRunning) {
      interval = setInterval(async () => {
        try {
          const executionId = await onCheckExecution(automation.id);
          if (executionId) {
            setIsListening(false);
            setIsTestRunning(true);

            // Fetch logs and visualize
            const logs = await onFetchStepLogs!(executionId);
            setTestLogs(logs);

            for (let i = 0; i < logs.length; i++) {
              setCurrentExecutingNode(logs[i].node_id);
              await new Promise(r => setTimeout(r, 800)); // Slightly slower for real-time feel
            }
            setIsTestRunning(false);
          }
        } catch (err) {
          console.error("Polling error:", err);
        }
      }, 2000);
    }
    return () => clearInterval(interval);
  }, [isListening, isTestRunning, automation?.id, onCheckExecution, onFetchStepLogs]);

  const handleTestRun = async () => {
    if (!automation?.id || !onTestRun || !onFetchStepLogs) return;

    setIsTestRunning(true);
    setTestLogs([]);
    setCurrentExecutingNode(null);

    try {
      const result = await onTestRun(automation.id, {});

      if (result.execution_id) {
        const logs = await onFetchStepLogs(result.execution_id);
        setTestLogs(logs);

        for (let i = 0; i < logs.length; i++) {
          setCurrentExecutingNode(logs[i].node_id);
          await new Promise(r => setTimeout(r, 500));
        }
      }
    } catch (err) {
      console.error('Test run failed:', err);
    } finally {
      setIsTestRunning(false);
    }
  };

  const nodeTypes = NODE_TYPES;
  const edgeTypes = EDGE_TYPES;

  const reorderNodes = useCallback((nds: Node[], sourceNodeId?: string, handleId?: string) => {
    if (sourceNodeId) {
      // Lê o sourceNode de nds (estado atual), não de closure stale
      const sourceNode = nds.find(n => n.id === sourceNodeId);
      if (sourceNode) {
        const def = NODE_LIBRARY.find(d => d.id === sourceNode.data.nodeId);
        if (def?.type === 'condition') {
          const outputs = sourceNode.data.outputs || def.outputs;
          const totalBranches = outputs.length;

          // Calcular índice do handle
          let handleIdx = 0;
          if (handleId === 'else' || handleId === 'false') {
            handleIdx = totalBranches - 1; // else/false é sempre o último
          } else if (handleId === 'true') {
            handleIdx = 0;
          } else if (handleId?.startsWith('route_')) {
            handleIdx = parseInt(handleId.replace('route_', '')) || 0;
          }

          const spread = Math.max(500, 400 * totalBranches);
          // Evitar divisão por zero quando totalBranches === 1
          const offsetX = totalBranches <= 1 ? 0
            : ((handleIdx / (totalBranches - 1)) - 0.5) * spread;

          return nds.map(n => {
            const isNew = n.id === nds[nds.length - 1].id;
            if (!isNew) return n;
            return {
              ...n,
              position: {
                x: sourceNode.position.x + offsetX,
                y: sourceNode.position.y + 450
              }
            };
          });
        }
      }
    }
    // Retorna os nós sem forçar alinhamento vertical
    return nds;
  }, []);

  const onAddNodeClick = (edgeId: string) => {
    setActiveEdgeId(edgeId);
    setIsLibraryOpen(true);
  };

  // Patch: edges criadas na inicialização têm onAddNode como noop (evita TDZ).
  // Após o mount, injetamos a função real.
  useEffect(() => {
    setEdges(eds => eds.map(e => ({
      ...e,
      data: {
        ...e.data,
        onAddNode: onAddNodeClick,
        onDeleteEdge: (edgeId: string) => setEdges(prev => prev.filter(edge => edge.id !== edgeId))
      }
    })));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const getAutoNodeLabel = useCallback((definition: NodeDefinition, sourceId?: string, handleId?: string) => {
    let routeName = '';
    if (sourceId) {
      const sourceNode = nodes.find(n => n.id === sourceId);
      if (sourceNode && sourceNode.data.nodeId === 'condition_if') {
        if (handleId === 'else' || handleId === 'false') {
          routeName = 'ELSE';
        } else if (handleId === 'true') {
          routeName = 'MATCH';
        } else if (handleId?.startsWith('route_')) {
          const routeIdx = parseInt(handleId.replace('route_', '')) || 0;
          const routes = sourceNode.data.config?.routes || [];
          routeName = routes[routeIdx]?.label || `ROTA ${routeIdx}`;
        }
      }
    }

    const baseLabel = routeName ? `${definition.label} ${routeName}` : definition.label;

    // Contagem para unicidade e sequência (ex: Action, Action 2, Action 3...)
    const existingLabels = nodes.map(n => n.data.label);
    let counter = 1;
    let finalLabel = baseLabel;

    while (existingLabels.includes(finalLabel)) {
      counter++;
      finalLabel = `${baseLabel} ${counter}`;
    }

    return finalLabel;
  }, [nodes]);

  const onNodeSelect = (definition: NodeDefinition) => {
    const newNodeId = generateId('node');

    // Melhoria: Herança inteligente de Schema/Table
    let inheritedConfig = {};
    if (definition.id === 'database_action') {
      // Se o nó anterior for um gatilho de sequestro, herdamos o schema e a tabela
      let sourceNode = null;
      if (activeEdgeId) {
        const edge = edges.find(e => e.id === activeEdgeId);
        sourceNode = nodes.find(n => n.id === edge?.source);
      } else if (nodes.length > 0) {
        sourceNode = nodes[nodes.length - 1];
      }

      if (sourceNode && (sourceNode.data.nodeId === 'pre_event_trigger' || sourceNode.data.nodeId === 'post_event_trigger')) {
        inheritedConfig = {
          schema: sourceNode.data.config?.schema,
          table: sourceNode.data.config?.table,
          operation: 'INSERT' // Default para actions vindas de gatilho
        };
      }
    } else if (definition.id === 'webhook_trigger') {
      inheritedConfig = {
        path_slug: `webhook-${Math.random().toString(36).substring(7)}`,
        auth_method: 'none'
      };
    } else if (definition.id === 'step_up_challenge_trigger') {
      inheritedConfig = {
        event: 'step_up_challenge_requested',
        provider: '*',
        exposed_variables: ['{{otp.code}}', '{{otp.provider}}', '{{user.identifier}}', '{{challenge.id}}']
      };
    }

    const onAddFromAntenna = (id: string, handleId: string) => {
      setActiveHandle({ nodeId: id, handleId });
      setIsLibraryOpen(true);
      setActiveEdgeId(null);
    };

    if (activeEdgeId) {
      const edge = edges.find(e => e.id === activeEdgeId);
      if (edge) {
        const sourceId = edge.source;
        const targetId = edge.target;
        const handleId = edge.sourceHandle || undefined;

        const autoLabel = getAutoNodeLabel(definition, sourceId, handleId);

        const newNode: Node = {
          id: newNodeId,
          type: 'nexusNode',
          position: { x: 400, y: 0 },
          data: {
            id: newNodeId,
            nodeId: definition.id,
            label: autoLabel,
            type: definition.type,
            config: inheritedConfig,
            outputs: definition.outputs,
            onAddFromAntenna
          }
        };

        const newEdges: Edge[] = [
          {
            id: `e-${sourceId}-${newNodeId}`,
            source: sourceId,
            target: newNodeId,
            sourceHandle: edge.sourceHandle,
            type: 'premiumEdge',
            label: edge.label, // Inherit label if splitting a condition edge
            data: {
              onAddNode: onAddNodeClick,
              onDeleteEdge: (edgeId: string) => setEdges(prev => prev.filter(edge => edge.id !== edgeId))
            }
          },
          {
            id: `e-${newNodeId}-${targetId}`,
            source: newNodeId,
            target: targetId,
            type: 'premiumEdge',
            data: {
              onAddNode: onAddNodeClick,
              onDeleteEdge: (edgeId: string) => setEdges(prev => prev.filter(edge => edge.id !== edgeId))
            }
          }
        ];

        const sourceIdx = nodes.findIndex(n => n.id === sourceId);
        const newNodes = [...nodes];
        newNodes.splice(sourceIdx + 1, 0, newNode);

        setNodes(reorderNodes(newNodes));
        setEdges(eds => eds.filter(e => e.id !== activeEdgeId).concat(newEdges));
      }
    } else if (activeHandle) {
      const autoLabel = getAutoNodeLabel(definition, activeHandle.nodeId, activeHandle.handleId);

      const newNode: Node = {
        id: newNodeId,
        type: 'nexusNode',
        position: { x: 400, y: 50 }, // Will be reordered
        data: {
          id: newNodeId,
          nodeId: definition.id,
          label: autoLabel,
          type: definition.type,
          config: inheritedConfig,
          outputs: definition.outputs,
          onAddFromAntenna
        }
      };

      const sourceNode = nodes.find(n => n.id === activeHandle.nodeId);
      const sourceDef = sourceNode ? NODE_LIBRARY.find(d => d.id === sourceNode.data.nodeId) : null;
      const isConditionSource = sourceDef?.type === 'condition';

      // Determina o label da rota para edges de condição
      let edgeLabel: string | undefined;
      if (isConditionSource) {
        if (activeHandle.handleId === 'else') {
          edgeLabel = 'ELSE';
        } else {
          const routeIdx = parseInt(activeHandle.handleId.replace('route_', '')) || 0;
          const routes = sourceNode?.data.config?.routes || [];
          edgeLabel = routes[routeIdx]?.label || `ROTA ${routeIdx}`;
        }
      }

      const newEdge: Edge = {
        id: `e-${activeHandle.nodeId}-${newNodeId}-${activeHandle.handleId || 'next'}`,
        source: activeHandle.nodeId,
        target: newNodeId,
        sourceHandle: activeHandle.handleId,
        type: 'premiumEdge',
        label: edgeLabel,
        data: {
          onAddNode: onAddNodeClick,
          onDeleteEdge: (edgeId: string) => setEdges(prev => prev.filter(edge => edge.id !== edgeId))
        }
      };

      setNodes(prev => reorderNodes([...prev, newNode], activeHandle.nodeId, activeHandle.handleId));
      setEdges(eds => eds.concat(newEdge));
      setActiveHandle(null);
    } else {
      const lastNode = nodes.length > 0 ? nodes[nodes.length - 1] : null;
      const autoLabel = getAutoNodeLabel(definition, lastNode?.id);

      const newNode: Node = {
        id: newNodeId,
        type: 'nexusNode',
        position: { x: 400, y: lastNode ? lastNode.position.y + 450 : 50 },
        data: {
          id: newNodeId,
          nodeId: definition.id,
          label: autoLabel,
          type: definition.type,
          config: inheritedConfig,
          outputs: definition.outputs, // garante que as antenas nascem corretas
          onAddFromAntenna
        }
      };

      if (lastNode) {
        const newEdge: Edge = {
          id: `e-${lastNode.id}-${newNodeId}-next`,
          source: lastNode.id,
          target: newNodeId,
          type: 'premiumEdge',
          data: {
            onAddNode: onAddNodeClick,
            onDeleteEdge: (edgeId: string) => setEdges(prev => prev.filter(edge => edge.id !== edgeId))
          }
        };
        setEdges(eds => eds.concat(newEdge));
      }

      setNodes(nds => nds.concat(newNode));
    }

    setIsLibraryOpen(false);
    setActiveEdgeId(null);
    setActiveHandle(null);
  };

  useEffect(() => {
    if (nodes.length === 0 && !isLibraryOpen) {
      setIsLibraryOpen(true);
    }
  }, [nodes.length]);

  // Sincronizar labels de edges quando rotas de condição mudam
  useEffect(() => {
    setEdges(eds => eds.map(edge => {
      const sourceNode = nodes.find(n => n.id === edge.source);
      const sourceDef = sourceNode ? NODE_LIBRARY.find(d => d.id === sourceNode.data.nodeId) : null;

      if (sourceDef?.type === 'condition' && edge.sourceHandle) {
        const routes = sourceNode?.data.config?.routes || [];
        let newLabel: string | undefined;

        if (edge.sourceHandle === 'true') {
          newLabel = 'DOES MATCH';
        } else if (edge.sourceHandle === 'false' || edge.sourceHandle === 'else') {
          newLabel = 'ELSE';
        } else if (edge.sourceHandle.startsWith('route_')) {
          const routeIdx = parseInt(edge.sourceHandle.replace('route_', '')) || 0;
          newLabel = routes[routeIdx]?.label || `ROTA ${routeIdx}`;
        }

        if (newLabel !== edge.label) {
          return { ...edge, label: newLabel };
        }
      }
      return edge;
    }));
  }, [nodes]);

  const onConnect = useCallback((params: Connection) => {
    // Detecta se a source é um nó condition e aplica label correto na edge
    const sourceNode = nodes.find(n => n.id === params.source);
    const sourceDef = sourceNode ? NODE_LIBRARY.find(d => d.id === sourceNode.data.nodeId) : null;
    let edgeLabel: string | undefined;
    if (sourceDef?.type === 'condition' && params.sourceHandle) {
      if (params.sourceHandle === 'else') {
        edgeLabel = 'ELSE';
      } else {
        const routeIdx = parseInt(params.sourceHandle.replace('route_', '')) || 0;
        const routes = sourceNode?.data.config?.routes || [];
        edgeLabel = routes[routeIdx]?.label || `ROTA ${routeIdx}`;
      }
    }
    const edge = {
      ...params,
      id: `e-${params.source}-${params.target}-${params.sourceHandle || 'next'}`,
      type: 'premiumEdge',
      label: edgeLabel,
      data: {
        onAddNode: onAddNodeClick,
        onDeleteEdge: (edgeId: string) => setEdges(prev => prev.filter(edge => edge.id !== edgeId))
      }
    };
    setEdges((eds) => addEdge(edge, eds));
  }, [nodes]);

  const onUpdateNode = useCallback((id: string, data: any) => {
    setNodes(nds => nds.map(n => {
      if (n.id !== id) return n;
      // Força remount do NexusNode quando outputs mudam (ReactFlow precisa re-registrar handles)
      const outputsChanged = JSON.stringify(n.data.outputs) !== JSON.stringify(data.outputs);
      return {
        ...n,
        // key trick: ReactFlow usa o id do nó internamente, mas podemos forçar re-render via data key
        data: {
          ...data,
          _handleKey: outputsChanged ? Date.now() : (n.data._handleKey || 0)
        }
      };
    }));
  }, [setNodes]);

  const onDeleteNode = useCallback((id: string) => {
    setNodes(nds => nds.filter(n => n.id !== id));
    setEdges(eds => eds.filter(e => e.source !== id && e.target !== id));
  }, [setNodes, setEdges]);

  const handleGlobalSave = () => {
    if (onSave) {
      const exportNodes = nodes.map(n => {
        // Para nós de condição, ordenar nextIds pela porta (route_0, route_1, ..., else)
        const nodeDef = NODE_LIBRARY.find(d => d.id === n.data.nodeId);
        const isCondition = nodeDef?.type === 'condition';

        let nextIds: string[];
        if (isCondition) {
          const routes = n.data.config?.routes || [];
          const routeCount = routes.length;
          const sortedEdges = edges
            .filter(e => e.source === n.id && e.sourceHandle)
            .sort((a, b) => {
              const getRouteIdx = (handle: string) => {
                if (handle === 'else' || handle === 'false') return routeCount; // else vem por último
                if (handle === 'true') return 0;
                return parseInt(handle.replace('route_', '')) || 0;
              };
              return getRouteIdx(a.sourceHandle || '') - getRouteIdx(b.sourceHandle || '');
            });
          nextIds = sortedEdges.map(e => e.target);
        } else {
          nextIds = edges
            .filter(e => e.source === n.id)
            .map(e => e.target);
        }

        return {
          id: n.id,
          node_id: n.data.nodeId,
          type: n.data.type,
          label: n.data.label,
          x: n.position.x,
          y: n.position.y,
          config: n.data.config || {},
          next: nextIds
        };
      });

      // Contrato Nexus v0: payload padronizado
      onSave({
        name: automationName,
        nodes: exportNodes,
        edges: edges.map(e => ({
          id: e.id,
          source: e.source,
          sourceHandle: e.sourceHandle,
          target: e.target,
          targetHandle: e.targetHandle
        })),
        execution_mode: executionMode
      });
    }
  };

  return (
    <div className="flex-1 h-full w-full bg-[#fcfdfe] relative flex overflow-hidden font-sans selection:bg-indigo-100 selection:text-indigo-900">

      {/* Editor Main Area */}
      <div className="flex-1 h-full relative">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          isValidConnection={(conn) => {
            // Lógica de validação inteligente em tempo de arraste
            const sourceNode = nodes.find(n => n.id === conn.source);
            const targetNode = nodes.find(n => n.id === conn.target);
            if (!sourceNode || !targetNode) return false;

            const sourceDef = NODE_LIBRARY.find(d => d.id === sourceNode.data.nodeId);
            const targetDef = NODE_LIBRARY.find(d => d.id === targetNode.data.nodeId);
            if (!sourceDef || !targetDef) return false;

            // Extrair o índice do port pela string do handle
            let sourcePortIdx = 0;
            if (conn.sourceHandle?.startsWith('out-')) {
              sourcePortIdx = parseInt(conn.sourceHandle.replace('out-', '')) || 0;
            } else if (conn.sourceHandle?.startsWith('route_')) {
              sourcePortIdx = parseInt(conn.sourceHandle.replace('route_', '')) || 0;
            } else if (conn.sourceHandle === 'else' || conn.sourceHandle === 'false') {
              // else é o último output
              const sourceOutputs = sourceNode.data.outputs || sourceDef.outputs;
              sourcePortIdx = sourceOutputs.length - 1;
            } else if (conn.sourceHandle === 'true') {
              sourcePortIdx = 0;
            }

            const targetPortIdx = conn.targetHandle?.startsWith('in-') ? parseInt(conn.targetHandle.replace('in-', '')) : 0;

            // Para nós de condição, usar outputs dinâmicos do nó
            const sourceOutputs = sourceDef.type === 'condition'
              ? (sourceNode.data.outputs || sourceDef.outputs)
              : sourceDef.outputs;
            const sourceType = sourceOutputs[sourcePortIdx] || 'any';
            const targetType = targetDef.inputs[targetPortIdx] || 'any';

            // Regras de anti-fragilidade
            if (sourceType === 'any' || targetType === 'any') return true;
            if (sourceType === targetType) return true;
            // TODO: Adicionar casts seguros (ex: number -> string)
            return false;
          }}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodeClick={(_, node) => setSelectedNode(node)}
          onPaneClick={() => setSelectedNode(null)}
          fitView
          minZoom={0.05}
          maxZoom={2.5}
          defaultViewport={{ x: 0, y: 0, zoom: 0.75 }}
        >
          <Background gap={40} color="#f1f5f9" variant="dots" size={1.5} />

          {/* Custom Header Panel */}
          <Panel position="top-left" className="m-10 flex items-center gap-8">
            <button
              onClick={onBack}
              className="w-16 h-16 bg-white rounded-3xl flex items-center justify-center text-slate-400 hover:text-slate-900 hover:scale-110 transition-all shadow-[0_20px_40px_-10px_rgba(0,0,0,0.1)] border border-slate-100 group"
            >
              <ArrowRight size={28} className="rotate-180 group-hover:-translate-x-1 transition-transform" />
            </button>
            <div className="bg-white/70 backdrop-blur-3xl p-8 rounded-[3rem] shadow-[0_30px_60px_-15px_rgba(0,0,0,0.12)] border border-white/50 flex items-center gap-6">
              <div className="w-16 h-16 bg-gradient-to-br from-indigo-600 to-purple-700 rounded-3xl flex items-center justify-center text-white shadow-[0_15px_30px_-5px_rgba(79,70,229,0.4)] relative overflow-hidden group">
                <div className="absolute inset-0 bg-white/20 translate-y-full group-hover:translate-y-0 transition-transform duration-500" />
                <BrainCircuit size={32} strokeWidth={2.5} className="relative z-10" />
              </div>
              <div>
                <div className="flex items-center gap-3 mb-1.5">
                  {isEditingName ? (
                    <input
                      autoFocus
                      type="text"
                      className="text-2xl font-black text-slate-900 tracking-tighter uppercase bg-indigo-50/50 border-b-2 border-indigo-500 outline-none px-2 rounded-t-lg"
                      value={automationName}
                      onChange={(e) => setAutomationName(e.target.value)}
                      onBlur={() => setIsEditingName(false)}
                      onKeyDown={(e) => e.key === 'Enter' && setIsEditingName(false)}
                    />
                  ) : (
                    <h2
                      onClick={() => setIsEditingName(true)}
                      className="text-2xl font-black text-slate-900 tracking-tighter uppercase cursor-edit hover:text-indigo-600 transition-colors"
                    >
                      {automationName}
                    </h2>
                  )}
                  <div className="flex items-center gap-2 px-3 py-1 bg-emerald-50 rounded-full border border-emerald-100">
                    <div className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
                    <span className="text-[9px] text-emerald-600 font-black uppercase tracking-widest">Active Architect</span>
                  </div>
                </div>
                <p className="text-[11px] text-slate-400 font-black uppercase tracking-[0.3em] opacity-60">Control Plane // Agentic Orchestration</p>
              </div>
              <div className="h-12 w-px bg-slate-100 mx-2" />
              <div className="flex bg-slate-100 p-1.5 rounded-2xl gap-1">
                <button
                  onClick={() => setExecutionMode('linear')}
                  className={`px-5 py-2.5 rounded-xl text-[9px] font-black uppercase tracking-widest transition-all flex items-center gap-2 ${executionMode === 'linear' ? 'bg-white shadow-md text-slate-900' : 'text-slate-400 hover:text-slate-600'}`}
                >
                  <ArrowRight size={12} /> Linear
                </button>
                <button
                  onClick={() => setExecutionMode('parallel')}
                  className={`px-5 py-2.5 rounded-xl text-[9px] font-black uppercase tracking-widest transition-all flex items-center gap-2 ${executionMode === 'parallel' ? 'bg-white shadow-md text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                >
                  <Zap size={12} /> Paralelo
                </button>
              </div>
            </div>
          </Panel>

          {/* Bottom Action Panel */}
          <Panel position="bottom-center" className="mb-14">
            <div className="bg-slate-900/95 backdrop-blur-3xl p-4 rounded-[3.5rem] shadow-[0_50px_100px_-20px_rgba(0,0,0,0.6)] border border-slate-800 flex items-center gap-4">
              <button onClick={() => setIsLibraryOpen(true)} className="px-10 py-6 bg-white text-slate-900 rounded-[2.5rem] font-black text-[12px] uppercase tracking-[0.2em] hover:scale-105 transition-all flex items-center gap-4 shadow-2xl group">
                <Plus size={20} strokeWidth={3} className="group-hover:rotate-90 transition-transform duration-500" /> Injetar Novo Nó
              </button>
              <div className="w-px h-12 bg-slate-800/50 mx-2" />
              <button onClick={handleGlobalSave} className="px-10 py-6 bg-indigo-600 text-white rounded-[2.5rem] font-black text-[12px] uppercase tracking-[0.2em] hover:bg-indigo-500 hover:scale-105 transition-all flex items-center gap-4 shadow-2xl shadow-indigo-900/30">
                <Save size={20} /> Salvar Blueprint
              </button>
              <button
                onClick={handleToggleListening}
                disabled={isTestRunning || !automation?.id}
                className={`w-20 h-20 rounded-full flex items-center justify-center hover:scale-110 transition-all shadow-2xl relative group 
                  ${isTestRunning ? 'bg-amber-500 animate-pulse shadow-amber-900/40' :
                    isListening ? 'bg-indigo-600 shadow-indigo-900/40' :
                      'bg-emerald-500 hover:bg-emerald-400 shadow-emerald-900/30'}`}
              >
                <div className={`absolute inset-0 bg-white/20 rounded-full scale-0 group-hover:scale-100 transition-transform duration-500`} />

                {/* Radar effect for Listening Mode */}
                {isListening && (
                  <>
                    <div className="absolute inset-0 rounded-full bg-indigo-500 animate-ping opacity-20" />
                    <div className="absolute inset-0 rounded-full bg-indigo-500 animate-ping opacity-10 delay-300" />
                  </>
                )}

                {isTestRunning ? (
                  <Loader2 size={32} className="relative z-10 animate-spin text-white" />
                ) : isListening ? (
                  <Radio size={32} className="relative z-10 text-white animate-pulse" />
                ) : (
                  <Play size={32} fill="white" className="relative z-10 ml-1 text-white" />
                )}

                {/* Tooltip for clarification */}
                <div className="absolute -top-12 left-1/2 -translate-x-1/2 bg-slate-900 text-white text-[9px] font-black uppercase tracking-widest px-3 py-1.5 rounded-lg opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none whitespace-nowrap border border-slate-700 shadow-2xl">
                  {isListening ? 'Interromper Escuta' : isTestRunning ? 'Executando...' : 'Ativar Modo Escuta'}
                </div>
              </button>
            </div>
          </Panel>

          {/* Real-time Execution Panel */}
          {testLogs.length > 0 && (
            <Panel position="top-right" className="m-10">
              <div className={`bg-slate-900/95 backdrop-blur-3xl rounded-[2.5rem] shadow-2xl border border-slate-700 overflow-hidden w-96 flex flex-col transition-all duration-500 ${isTelemetryMinimized ? 'max-h-[72px]' : 'max-h-[70vh]'}`}>
                <div className="px-6 py-4 bg-slate-800/50 border-b border-slate-700 flex items-center justify-between cursor-pointer hover:bg-slate-800/80 transition-colors" onClick={() => setIsTelemetryMinimized(!isTelemetryMinimized)}>
                  <div className="flex items-center gap-3">
                    <div className={`w-2.5 h-2.5 rounded-full ${isTestRunning ? 'bg-amber-500 animate-pulse' : isListening ? 'bg-indigo-500 animate-pulse' : 'bg-emerald-500'}`} />
                    <span className="text-xs font-black text-white uppercase tracking-widest">
                      {isListening ? 'Aguardando Requisição...' : 'Nexus Telemetry'}
                    </span>
                  </div>
                  <div className="flex items-center gap-3">
                    <span className="text-[10px] font-mono text-slate-400">{testLogs.length} steps</span>
                    <button className="text-slate-500 hover:text-white transition-colors">
                      {isTelemetryMinimized ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
                    </button>
                  </div>
                </div>
                {!isTelemetryMinimized && (
                  <div className="flex-1 overflow-y-auto p-4 space-y-3 custom-scrollbar animate-in fade-in slide-in-from-top-2 duration-300">
                    {testLogs.map((log, idx) => (
                      <div
                        key={log.step_id || idx}
                        className={`p-4 rounded-2xl transition-all ${currentExecutingNode === log.node_id
                          ? 'bg-indigo-600/30 border-2 border-indigo-500 shadow-lg shadow-indigo-500/20'
                          : log.level === 'error'
                            ? 'bg-rose-900/30 border border-rose-700'
                            : 'bg-slate-800/50 border border-slate-700'
                          }`}
                      >
                        <div className="flex items-center justify-between mb-2">
                          <div className="flex items-center gap-2">
                            <span className={`text-[9px] font-black uppercase tracking-wider px-2 py-1 rounded-lg ${log.level === 'error' ? 'bg-rose-500/20 text-rose-400' : 'bg-emerald-500/20 text-emerald-400'
                              }`}>{log.level}</span>
                            <span className="text-[10px] font-bold text-slate-300 uppercase">{log.node_name || 'Node'}</span>
                          </div>
                          <span className="text-[9px] font-mono text-slate-500">{log.duration_ms}ms</span>
                        </div>
                        <p className="text-[11px] text-slate-400 leading-relaxed line-clamp-2">{log.message}</p>

                        {log.level === 'error' && log.error_details && (
                          <div className="mt-3 p-3 bg-rose-500/10 border border-rose-500/20 rounded-xl">
                            <div className="flex items-center gap-2 mb-1">
                              <ShieldAlert size={10} className="text-rose-400" />
                              <span className="text-[9px] font-black text-rose-400 uppercase tracking-widest">Error Details</span>
                            </div>
                            <p className="text-[10px] text-rose-300 font-mono break-words leading-relaxed">
                              {log.error_details}
                            </p>
                          </div>
                        )}

                        {log.input_data && (
                          <details className="mt-2">
                            <summary className="text-[9px] text-indigo-400 cursor-pointer hover:text-indigo-300">Input Data</summary>
                            <pre className="text-[9px] text-emerald-400 mt-1 bg-slate-950/50 p-2 rounded-lg overflow-x-auto">{JSON.stringify(log.input_data, null, 2)}</pre>
                          </details>
                        )}
                        {log.output_data && (
                          <details className="mt-2">
                            <summary className="text-[9px] text-purple-400 cursor-pointer hover:text-purple-300">Output Data</summary>
                            <pre className="text-[9px] text-indigo-300 mt-1 bg-slate-950/50 p-2 rounded-lg overflow-x-auto">{JSON.stringify(log.output_data, null, 2)}</pre>
                          </details>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </Panel>
          )}

          <Controls className="m-10 bg-white/60 backdrop-blur-3xl border-white shadow-2xl rounded-3xl overflow-hidden scale-125 border" />

          <MiniMap
            position="bottom-left"
            className="m-10 bg-white/30 backdrop-blur-2xl border-white/40 shadow-2xl rounded-[3rem] overflow-hidden border"
            nodeStrokeWidth={4}
            maskColor="rgba(241, 245, 249, 0.5)"
            nodeColor={(n) => {
              const def = NODE_LIBRARY.find(d => d.id === n.data.nodeId);
              return def ? (def.color.includes('amber') ? '#f59e0b' : def.color.includes('indigo') ? '#6366f1' : '#10b981') : '#cbd5e1';
            }}
          />
        </ReactFlow>

        {/* Modal Selection */}
        <NodeLibraryModal
          isOpen={isLibraryOpen}
          onClose={() => setIsLibraryOpen(false)}
          onSelect={onNodeSelect}
          hasTrigger={nodes.some(n => n.data.type === 'trigger')}
        />
      </div>

      {/* Sidebar / Configuration Drawer */}
      {selectedNode && (
        <ConfigDrawer
          node={nodes.find((n: Node) => n.id === selectedNode.id) || selectedNode}
          allNodes={nodes}
          projectId={projectId}
          onUpdate={onUpdateNode}
          onDelete={onDeleteNode}
          onClose={() => setSelectedNode(null)}
          testLogs={testLogs}
        />
      )}
    </div>
  );
};

const NexusArchitect: React.FC<NexusArchitectProps> = (props) => (
  <ReactFlowProvider>
    <NexusArchitectContent {...props} />
  </ReactFlowProvider>
);

export default NexusArchitect;
