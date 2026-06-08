import React from 'react';
import {
   Activity, AlertCircle, CheckCircle2,
   History, Layout, Loader2, Play, Plus,
   Settings, Terminal, Trash2, Workflow, X, Zap,
   ChevronRight, Shield, Cpu, Layers
} from 'lucide-react';
import type { Automation, ExecutionRun } from './types';

interface OverviewProps {
   success: string | null;
   error: string | null;
   activeTab: 'workflows' | 'runs';
   setActiveTab: (tab: 'workflows' | 'runs') => void;
   handleCreateNew: () => void;
   loading: boolean;
   automations: Automation[];
   handleOpenExisting: (auto: Automation) => void;
   handleDelete: (id: string) => void;
   handleActivate: (auto: Automation) => void;
   handleDeactivate: (auto: Automation) => void;
   handleToggle: (auto: Automation) => void;
   stats: Record<string, any>;
   fetchRuns: (id?: string) => void;
   fetchStepLogs: (id: string) => void;
   runs: ExecutionRun[];
   setSelectedExecutionId: (id: string | null) => void;
   selectedExecutionId: string | null;
   setStepLogs: (logs: any[]) => void;
   stepLogs: any[];
   loadingStepLogs: boolean;
   conflictModal: any;
   setConflictModal: (val: any) => void;
   submitting: boolean;
}

const AutomationOverviewView: React.FC<OverviewProps> = (props) => {
   const {
      success, error, activeTab, setActiveTab, handleCreateNew, loading,
      automations, handleOpenExisting, handleDelete,
      handleActivate, handleDeactivate, handleToggle, stats,
      fetchRuns, fetchStepLogs, runs, setSelectedExecutionId, selectedExecutionId,
      setStepLogs, stepLogs, loadingStepLogs, conflictModal, setConflictModal, submitting
   } = props;

   return (
      <div className="space-y-12 animate-in fade-in slide-in-from-bottom-4 duration-700">

         {/* Premium Notifications */}
         {(success || error) && (
            <div className={`fixed top-12 left-1/2 -translate-x-1/2 z-[1000] px-10 py-5 rounded-[2rem] shadow-[0_30px_60px_-15px_rgba(0,0,0,0.3)] flex items-center gap-4 animate-in slide-in-from-top-8 ${error ? 'bg-rose-600' : 'bg-slate-900'} text-white border border-white/10 backdrop-blur-xl`}>
               {error ? <AlertCircle size={22} /> : <Zap size={22} className="text-indigo-400" />}
               <span className="text-xs font-black uppercase tracking-[0.2em]">{success || error}</span>
            </div>
         )}

         {/* Hero Header */}
         <header className="flex flex-col md:flex-row md:items-center justify-between gap-8">
            <div>
               <h1 className="text-4xl font-black text-slate-900 tracking-tighter uppercase mb-2">Nexus Orchestrator</h1>
               <p className="text-xs font-bold text-slate-400 uppercase tracking-[0.3em] flex items-center gap-2">
                  <Shield size={14} className="text-indigo-500" /> Layer-4 Edge Defense // Autonomous Flow Engine
               </p>
            </div>

            <div className="flex items-center gap-4">
               <div className="flex bg-slate-100 p-1.5 rounded-[2rem] shadow-inner border border-slate-200/50">
                  <button
                     onClick={() => setActiveTab('workflows')}
                     className={`px-8 py-3 rounded-[1.5rem] text-[10px] font-black uppercase tracking-widest transition-all ${activeTab === 'workflows' ? 'bg-white text-slate-900 shadow-xl' : 'text-slate-400 hover:text-slate-600'}`}
                  >
                     Blueprints
                  </button>
                  <button
                     onClick={() => setActiveTab('runs')}
                     className={`px-8 py-3 rounded-[1.5rem] text-[10px] font-black uppercase tracking-widest transition-all ${activeTab === 'runs' ? 'bg-white text-slate-900 shadow-xl' : 'text-slate-400 hover:text-slate-600'}`}
                  >
                     Telemetria
                  </button>
               </div>

               <button
                  onClick={handleCreateNew}
                  className="bg-slate-900 text-white px-10 py-5 rounded-[2.5rem] font-black text-[11px] uppercase tracking-[0.2em] flex items-center gap-4 hover:bg-black transition-all shadow-[0_20px_40px_-10px_rgba(0,0,0,0.3)] hover:scale-[1.03] active:scale-95 group"
               >
                  <div className="w-6 h-6 bg-indigo-500 rounded-lg flex items-center justify-center group-hover:rotate-90 transition-transform duration-500">
                     <Plus size={16} strokeWidth={3} />
                  </div>
                  Novo Blueprint
               </button>
            </div>
         </header>

         {loading ? (
            <div className="py-60 flex flex-col items-center justify-center text-slate-200">
               <div className="relative">
                  <Cpu size={80} className="animate-pulse text-indigo-100" />
                  <div className="absolute inset-0 flex items-center justify-center">
                     <Loader2 size={32} className="animate-spin text-indigo-500" />
                  </div>
               </div>
               <p className="text-[11px] font-black uppercase tracking-[0.4em] mt-8 animate-pulse text-slate-400">Sincronizando Nexus Engine...</p>
            </div>
         ) : activeTab === 'workflows' ? (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-8">
               {automations.map((auto: Automation) => (
                  <div key={auto.id} className="bg-white border border-slate-100 rounded-[3.5rem] p-10 shadow-sm hover:shadow-[0_40px_80px_-20px_rgba(0,0,0,0.08)] transition-all group relative overflow-hidden flex flex-col h-full">
                     <div className="flex items-start justify-between mb-10">
                        <div className={`w-16 h-16 rounded-[1.5rem] flex items-center justify-center shadow-2xl transition-transform group-hover:scale-110 duration-500 ${auto.is_active ? 'bg-indigo-600 text-white shadow-indigo-200' : 'bg-slate-100 text-slate-300'}`}>
                           <Layers size={28} />
                        </div>
                        <div className="flex items-center gap-2">
                           <button
                              onClick={() => handleOpenExisting(auto)}
                              className="w-12 h-12 bg-slate-50 hover:bg-indigo-50 rounded-2xl text-slate-300 hover:text-indigo-600 transition-all flex items-center justify-center border border-transparent hover:border-indigo-100"
                           >
                              <Settings size={20} />
                           </button>
                           <button
                              onClick={() => handleDelete(auto.id)}
                              className="w-12 h-12 bg-slate-50 hover:bg-rose-50 rounded-2xl text-slate-300 hover:text-rose-600 transition-all flex items-center justify-center border border-transparent hover:border-rose-100"
                           >
                              <Trash2 size={20} />
                           </button>
                        </div>
                     </div>

                     <h4 className="text-2xl font-black text-slate-900 mb-3 truncate uppercase tracking-tighter group-hover:text-indigo-600 transition-colors">{auto.name}</h4>
                     <p className="text-xs text-slate-400 font-bold leading-relaxed mb-10 line-clamp-2 min-h-[3rem]">{auto.description || 'Nenhuma descrição fornecida.'}</p>

                     {/* Stats Strip */}
                     <div className="flex flex-wrap gap-3 mb-10 pt-8 border-t border-slate-50">
                        {(() => {
                           const s = stats[auto.id] || {};
                           const total = s.total_runs || 0;
                           const avg = s.avg_ms || 0;
                           return (
                              <>
                                 <div className="px-4 py-2 bg-slate-50 rounded-xl flex items-center gap-2 border border-slate-100">
                                    <Activity size={12} className="text-indigo-500" />
                                    <span className="text-[10px] font-black text-slate-600 uppercase tracking-widest">{total} Exec</span>
                                 </div>
                                 <div className="px-4 py-2 bg-slate-50 rounded-xl flex items-center gap-2 border border-slate-100">
                                    <Zap size={12} className="text-amber-500" />
                                    <span className="text-[10px] font-black text-slate-600 uppercase tracking-widest">{avg}ms</span>
                                 </div>
                              </>
                           );
                        })()}
                     </div>

                     <div className="mt-auto flex items-center justify-between gap-4">
                        <div className="flex items-center gap-3">
                           <div className={`w-3 h-3 rounded-full ${auto.status === 'active' ? 'bg-emerald-500 shadow-[0_0_15px_#10b981]' : 'bg-slate-300'}`} />
                           <span className="text-[10px] font-black text-slate-400 uppercase tracking-[0.2em]">{auto.status || 'Draft'}</span>
                        </div>

                        <div className="flex items-center gap-2">
                           {auto.status === 'active' ? (
                              <button
                                 onClick={() => handleDeactivate(auto)}
                                 disabled={submitting}
                                 className="px-6 py-3 bg-slate-900 text-white rounded-[1.2rem] text-[9px] font-black uppercase tracking-widest hover:bg-black transition-all flex items-center gap-2"
                              >
                                 <AlertCircle size={14} /> Desativar
                              </button>
                           ) : (
                              <button
                                 onClick={() => handleActivate(auto)}
                                 disabled={submitting}
                                 className="px-6 py-3 bg-indigo-600 text-white rounded-[1.2rem] text-[9px] font-black uppercase tracking-widest hover:bg-indigo-500 transition-all shadow-lg shadow-indigo-100 flex items-center gap-2"
                              >
                                 <Play size={14} fill="white" /> Ativar
                              </button>
                           )}
                           <button
                              onClick={() => { fetchRuns(auto.id); setActiveTab('runs'); }}
                              className="w-12 h-12 bg-slate-50 rounded-2xl flex items-center justify-center text-slate-400 hover:text-slate-900 transition-all border border-slate-100"
                           >
                              <History size={18} />
                           </button>
                        </div>
                     </div>
                  </div>
               ))}

               <button
                  onClick={handleCreateNew}
                  className="col-span-1 border-4 border-dashed border-slate-100 rounded-[3.5rem] flex flex-col items-center justify-center p-12 text-slate-300 hover:border-indigo-100 hover:text-indigo-200 transition-all group"
               >
                  <Plus size={48} strokeWidth={1} className="mb-6 group-hover:scale-125 transition-transform" />
                  <p className="text-[11px] font-black uppercase tracking-[0.3em]">Novo Blueprint Nexus</p>
               </button>
            </div>
         ) : (
            <div className="bg-white border border-slate-100 rounded-[4rem] overflow-hidden shadow-2xl animate-in fade-in slide-in-from-bottom-8 duration-700">
               <div className="p-10 border-b border-slate-50 bg-slate-50/30 flex items-center justify-between">
                  <h3 className="text-xl font-black text-slate-900 tracking-tight uppercase">Fluxo de Telemetria</h3>
                  <div className="flex gap-2">
                     <span className="px-4 py-2 bg-emerald-100 text-emerald-600 rounded-full text-[9px] font-black uppercase tracking-widest">Live Feed</span>
                  </div>
               </div>
               <table className="w-full text-left">
                  <thead>
                     <tr className="bg-slate-50/50 border-b border-slate-100 text-[10px] font-black text-slate-400 uppercase tracking-[0.3em]">
                        <th className="px-12 py-10">Status Engine</th>
                        <th className="px-12 py-10">Timestamp</th>
                        <th className="px-12 py-10">Latência Real</th>
                        <th className="px-12 py-10 text-right">Ação</th>
                     </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-50">
                     {runs.map((run: ExecutionRun) => (
                        <tr key={run.id} className="hover:bg-slate-50/40 transition-all group">
                           <td className="px-12 py-10">
                              <div className="flex items-center gap-4">
                                 <div className={`w-3 h-3 rounded-full ${run.status === 'success' ? 'bg-emerald-500 shadow-[0_0_10px_#10b981]' : 'bg-rose-500 shadow-[0_0_10px_#f43f5e]'}`} />
                                 <span className={`text-[11px] font-black uppercase tracking-widest ${run.status === 'success' ? 'text-emerald-600' : 'text-rose-600'}`}>{run.status}</span>
                              </div>
                           </td>
                           <td className="px-12 py-10 font-mono text-xs text-slate-400">{new Date(run.created_at).toLocaleString()}</td>
                           <td className="px-12 py-10">
                              <div className="flex items-center gap-4">
                                 <div className="w-24 h-2 bg-slate-100 rounded-full overflow-hidden">
                                    <div className="h-full bg-indigo-500 transition-all duration-1000" style={{ width: `${Math.min(run.execution_time_ms / 10, 100)}%` }}></div>
                                 </div>
                                 <span className="font-mono text-xs text-slate-900 font-bold">{run.execution_time_ms}ms</span>
                              </div>
                           </td>
                           <td className="px-12 py-10 text-right">
                              <button
                                 onClick={() => { setSelectedExecutionId(run.id); fetchStepLogs(run.id); }}
                                 className="px-6 py-3 bg-white border-2 border-slate-100 rounded-2xl text-[10px] font-black text-slate-900 uppercase tracking-widest hover:bg-slate-900 hover:text-white transition-all shadow-sm"
                              >
                                 Inspecionar Logs
                              </button>
                           </td>
                        </tr>
                     ))}
                     {runs.length === 0 && (
                        <tr>
                           <td colSpan={4} className="py-40 text-center text-slate-300">
                              <History size={48} className="mx-auto mb-6 opacity-10" />
                              <p className="text-xs font-black uppercase tracking-widest">Nenhuma execução registrada</p>
                           </td>
                        </tr>
                     )}
                  </tbody>
               </table>
            </div>
         )}

         {/* Log Inspector Modal */}
         {selectedExecutionId && (
            <div className="fixed inset-0 z-[2000] flex items-center justify-center bg-slate-900/90 backdrop-blur-md p-8 animate-in fade-in duration-500">
               <div className="bg-white w-full max-w-7xl h-full max-h-[90vh] rounded-[4rem] shadow-2xl overflow-hidden flex flex-col border border-white/20 animate-in zoom-in-95 duration-500">
                  <div className="px-12 py-10 border-b border-slate-100 bg-slate-50/50 flex items-center justify-between">
                     <div className="flex items-center gap-6">
                        <div className="w-16 h-16 bg-indigo-100 rounded-[1.5rem] flex items-center justify-center">
                           <Terminal size={32} className="text-indigo-600" />
                        </div>
                        <div>
                           <h3 className="text-2xl font-black text-slate-900 uppercase tracking-tight">Nexus Telemetry Insight</h3>
                           <p className="text-[11px] text-slate-400 font-bold uppercase tracking-[0.3em] mt-1.5">Trace ID: {selectedExecutionId}</p>
                        </div>
                     </div>
                     <button onClick={() => { setSelectedExecutionId(null); setStepLogs([]); }} className="w-14 h-14 hover:bg-slate-200 rounded-full flex items-center justify-center transition-all"><X size={32} /></button>
                  </div>

                  <div className="flex-1 overflow-y-auto p-12 bg-slate-50 custom-scrollbar">
                     {loadingStepLogs ? (
                        <div className="flex items-center justify-center h-full"><Loader2 size={48} className="animate-spin text-indigo-600" /></div>
                     ) : (
                        <div className="space-y-6">
                           {stepLogs.map((log) => (
                              <div key={log.step_id} className={`rounded-[2.5rem] border-2 p-8 transition-all hover:shadow-2xl ${log.level === 'error' ? 'bg-rose-50 border-rose-100' : 'bg-white border-slate-100'}`}>
                                 <div className="flex items-center justify-between mb-6">
                                    <div className="flex items-center gap-4">
                                       <span className={`px-4 py-2 rounded-xl text-[10px] font-black uppercase tracking-widest ${log.level === 'error' ? 'bg-rose-200 text-rose-800' : 'bg-emerald-100 text-emerald-800'}`}>{log.level}</span>
                                       <span className="text-xs font-black text-slate-900 uppercase tracking-widest">{log.node_name || 'Anonymous Node'}</span>
                                       <div className="w-1 h-1 rounded-full bg-slate-300" />
                                       <span className="text-[10px] font-bold text-slate-400 uppercase tracking-widest">{log.node_type}</span>
                                    </div>
                                    <span className="text-xs font-mono text-slate-400 font-bold">{log.duration_ms}ms // {new Date(log.created_at).toLocaleTimeString()}</span>
                                 </div>
                                 <p className="text-base text-slate-700 font-medium mb-8 leading-relaxed">"{log.message}"</p>
                                 {(log.input_data || log.output_data) && (
                                    <div className="grid grid-cols-2 gap-6">
                                       {log.input_data && (
                                          <div className="bg-slate-900 rounded-[2rem] p-6 shadow-xl"><p className="text-[10px] font-black text-indigo-400 uppercase tracking-widest mb-4">Input Data</p><pre className="text-xs text-emerald-400 font-mono overflow-x-auto whitespace-pre-wrap">{JSON.stringify(log.input_data, null, 2)}</pre></div>
                                       )}
                                       {log.output_data && (
                                          <div className="bg-slate-900 rounded-[2rem] p-6 shadow-xl"><p className="text-[10px] font-black text-purple-400 uppercase tracking-widest mb-4">Output Result</p><pre className="text-xs text-indigo-300 font-mono overflow-x-auto whitespace-pre-wrap">{JSON.stringify(log.output_data, null, 2)}</pre></div>
                                       )}
                                    </div>
                                 )}
                              </div>
                           ))}
                        </div>
                     )}
                  </div>
               </div>
            </div>
         )}
      </div>
   );
};

export default AutomationOverviewView;