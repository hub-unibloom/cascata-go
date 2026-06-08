
import React, { useState, useEffect } from 'react';
import { 
  GitMerge, Loader2, X, CheckCircle2,
  ShieldCheck, Plus, GitCompare, ChevronRight, ArrowRight, Rocket,
  Zap, Code2
} from 'lucide-react';

interface DeployWizardProps {
  projectId: string;
  onClose: () => void;
  onSuccess: () => void;
}

interface BranchRecord {
  id: string;
  name: string;
  branch_type: string;
  is_main: boolean;
}

const DeployWizard: React.FC<DeployWizardProps> = ({ projectId, onClose, onSuccess }) => {
  const normalizeDiff = (payload: any) => {
    const raw = payload?.diff || payload?.diff_result || {};
    const sqlList = Array.isArray(raw?.sql) ? raw.sql : [];
    return {
      ...raw,
      generated_sql: raw?.generated_sql || sqlList.join('\n\n'),
      data_summary: raw?.data_summary || [],
      policies: raw?.policies || [],
    };
  };
  // STATE
  const [step, setStep] = useState<'strategy' | 'diff' | 'executing' | 'success'>('strategy');
  const [diffData, setDiffData] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [branches, setBranches] = useState<BranchRecord[]>([]);
  const [selectedSourceBranch, setSelectedSourceBranch] = useState<string>('');
  // UI Tabs for Diff View
  const [activeTab, setActiveTab] = useState<'schema' | 'security' | 'sql'>('schema');

  useEffect(() => {
    const loadBranches = async () => {
      try {
        const res = await fetch(`/api/data/${projectId}/branch/list`, {
          headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
        });
        const data = await res.json().catch(() => null);
        if (!res.ok) throw new Error(data?.error || 'Failed to load branches.');
        const envBranches = Array.isArray(data?.branches)
          ? data.branches.filter((branch: BranchRecord) => branch.branch_type === 'environment')
          : [];
        setBranches(envBranches);

        const defaultSource = envBranches.find((branch: BranchRecord) => !branch.is_main);
        setSelectedSourceBranch(defaultSource?.name || '');
      } catch (e) {
        setError('Failed to load branches.');
      }
    };

    loadBranches();
  }, [projectId]);

  // FETCH DIFF
  useEffect(() => {
    if (step === 'diff' && selectedSourceBranch) {
        const loadDiff = async () => {
            setLoading(true);
            setError(null);
            try {
                const res = await fetch(`/api/data/${projectId}/branch/diff?source=${encodeURIComponent(selectedSourceBranch)}&target=main`, {
                    headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
                });
                const data = await res.json();
                if (!res.ok) throw new Error(data?.error || 'Failed to load diff.');
                const normalized = normalizeDiff(data);
                setDiffData(normalized);
            } catch (e: any) { setError(e?.message || "Failed to load diff."); }
            finally { setLoading(false); }
        };
        loadDiff();
    }
  }, [step, projectId, selectedSourceBranch]);

  // EXECUTE DEPLOY
  const handleDeploy = async () => {
      setStep('executing');
      try {
          if (!selectedSourceBranch) {
              setError('Create or select an environment branch before deploying.');
              setStep('strategy');
              return;
          }
          const res = await fetch(`/api/data/${projectId}/branch/deploy`, {
              method: 'POST',
              headers: { 
                  'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`, 
                  'Content-Type': 'application/json' 
              },
              body: JSON.stringify({ 
                  source_branch: selectedSourceBranch,
                  target_branch: 'main',
                  dry_run: false,
                  safety_snapshot: true
              })
          });
          const body = await res.json().catch(() => null);

          if (!res.ok) {
              const errorMsg = body?.error || body?.detail || `Deploy failed with status ${res.status}`;
              setError(errorMsg);
              setStep('diff');
              return;
          }

          setStep('success');
      } catch (e: any) {
          setError(e.message || "Deploy failed: Network error.");
          setStep('diff');
      }
  };

  return (
    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[900] flex items-center justify-center p-8 animate-in zoom-in-95 font-sans">
        <div className="bg-white rounded-[2.5rem] w-full max-w-5xl h-[85vh] flex flex-col shadow-2xl border border-slate-200 overflow-hidden relative">
            
            {/* HEADER */}
            <div className="p-8 border-b border-slate-100 flex justify-between items-center bg-slate-50/50">
                <div className="flex items-center gap-4">
                    <div className="w-14 h-14 rounded-2xl flex items-center justify-center text-white shadow-lg bg-indigo-600">
                        <GitMerge size={24}/>
                    </div>
                    <div>
                        <h3 className="text-2xl font-black text-slate-900 tracking-tight">Deploy Manager</h3>
                        <div className="flex items-center gap-2 mt-1">
                            {['Strategy', 'Schema Diff', 'Execute'].map((s, i) => (
                                <div key={s} className={`flex items-center text-[10px] font-bold uppercase tracking-widest ${
                                    ['strategy', 'diff', 'executing', 'success'].indexOf(step) >= i ? 'text-indigo-600' : 'text-slate-300'
                                }`}>
                                    {i > 0 && <ChevronRight size={12} className="mx-1 text-slate-300"/>}
                                    {s}
                                </div>
                            ))}
                        </div>
                    </div>
                </div>
                <button onClick={onClose} className="p-3 hover:bg-slate-200 rounded-full text-slate-400"><X size={20}/></button>
            </div>

            {/* BODY */}
            <div className="flex-1 overflow-y-auto p-8 bg-[#FAFBFC]">
                
                {/* STEP 1: STRATEGY SELECTION */}
                {step === 'strategy' && (
                    <div className="h-full flex flex-col items-center justify-center gap-8">
                        <h2 className="text-3xl font-black text-slate-900 text-center max-w-md">Select a branch to merge into main</h2>
                        <div className="w-full max-w-3xl bg-white border border-slate-200 rounded-[1.5rem] p-6">
                            <label className="block text-[10px] font-black uppercase tracking-widest text-slate-500 mb-3">Source Branch</label>
                            <select
                                value={selectedSourceBranch}
                                onChange={(e) => setSelectedSourceBranch(e.target.value)}
                                className="w-full rounded-2xl border border-slate-200 bg-slate-50 px-4 py-3 text-sm font-bold text-slate-800 outline-none"
                            >
                                <option value="">Select an environment branch</option>
                                {branches.filter((branch) => !branch.is_main).map((branch) => (
                                    <option key={branch.id} value={branch.name}>{branch.name}</option>
                                ))}
                            </select>
                            {branches.filter((branch) => !branch.is_main).length === 0 && (
                                <p className="mt-3 text-xs font-medium text-rose-600">
                                    No environment branch is available yet. Create one before deploying to `main`.
                                </p>
                            )}
                        </div>
                        <div className="w-full max-w-3xl">
                            <button 
                                onClick={() => { if (selectedSourceBranch) { setStep('diff'); } else { setError('Select a source branch first.'); } }}
                                className="group relative p-8 bg-white border-2 border-slate-200 rounded-[2rem] hover:border-indigo-500 hover:shadow-xl transition-all text-left flex flex-col w-full"
                            >
                                <div className="w-12 h-12 bg-indigo-50 rounded-2xl flex items-center justify-center text-indigo-600 mb-4 group-hover:scale-110 transition-transform"><GitMerge size={24}/></div>
                                <h4 className="text-lg font-black text-slate-900 mb-2">Transactional Merge</h4>
                                <p className="text-sm text-slate-500 font-medium leading-relaxed">
                                    Review the schema diff, inspect generated SQL, and merge the selected environment branch into `main` with safety snapshot enabled.
                                </p>
                                <span className="mt-6 text-xs font-black text-indigo-600 uppercase tracking-widest flex items-center gap-2">Analyze Diff <ArrowRight size={12}/></span>
                            </button>
                        </div>
                        {error && <p className="text-sm font-medium text-rose-600">{error}</p>}
                    </div>
                )}

                {/* STEP 2: SCHEMA DIFF */}
                {step === 'diff' && (
                    <div className="space-y-6">
                        {loading ? <div className="py-20 flex justify-center"><Loader2 className="animate-spin text-indigo-600" size={40}/></div> : (
                            <>
                                <div className="flex bg-slate-100 p-1 rounded-xl w-fit">
                                    <button onClick={() => setActiveTab('schema')} className={`px-6 py-2 rounded-lg text-xs font-black uppercase tracking-widest transition-all ${activeTab==='schema' ? 'bg-white shadow text-indigo-600' : 'text-slate-400'}`}>Schema</button>
                                    <button onClick={() => setActiveTab('security')} className={`px-6 py-2 rounded-lg text-xs font-black uppercase tracking-widest transition-all ${activeTab==='security' ? 'bg-white shadow text-indigo-600' : 'text-slate-400'}`}>Security</button>
                                    <button onClick={() => setActiveTab('sql')} className={`px-6 py-2 rounded-lg text-xs font-black uppercase tracking-widest transition-all ${activeTab==='sql' ? 'bg-white shadow text-indigo-600' : 'text-slate-400'}`}>Raw SQL</button>
                                </div>
                                
                                <div className="bg-white border border-slate-200 rounded-[2rem] p-8 min-h-[400px]">
                                    {activeTab === 'schema' && (
                                        <div className="space-y-4">
                                            {/* --- Tables --- */}
                                            {diffData?.added_tables?.map((t: string) => (
                                                <div key={t} className="bg-emerald-50 border border-emerald-100 p-4 rounded-xl flex items-center gap-3">
                                                    <div className="w-8 h-8 bg-emerald-100 rounded-lg flex items-center justify-center text-emerald-600"><Plus size={16}/></div>
                                                    <span className="font-bold text-emerald-900 text-sm">New Table: {t}</span>
                                                </div>
                                            ))}
                                            {diffData?.modified_tables?.map((m: any) => (
                                                <div key={m.table} className="bg-white border border-indigo-100 p-4 rounded-xl shadow-sm">
                                                    <div className="font-bold text-sm text-slate-800 mb-2 flex items-center gap-2"><GitCompare size={14} className="text-indigo-500"/> {m.table}</div>
                                                    <div className="flex gap-2 flex-wrap">
                                                        {m.added_cols?.map((c: string) => <span key={c} className="text-[10px] bg-emerald-50 text-emerald-700 px-2 py-1 rounded font-bold">+ {c}</span>)}
                                                        {m.renamed_cols?.map((c: any) => <span key={c.from} className="text-[10px] bg-amber-50 text-amber-700 px-2 py-1 rounded font-bold">{c.from} ➔ {c.to}</span>)}
                                                    </div>
                                                </div>
                                            ))}

                                            {/* --- Functions / RPCs --- */}
                                            {diffData?.added_functions?.map((f: any) => (
                                                <div key={`af-${f.name}-${f.signature}`} className="bg-emerald-50 border border-emerald-100 p-4 rounded-xl flex items-center gap-3">
                                                    <div className="w-8 h-8 bg-emerald-100 rounded-lg flex items-center justify-center text-emerald-600"><Code2 size={16}/></div>
                                                    <div>
                                                        <span className="font-bold text-emerald-900 text-sm">New Function: {f.name}</span>
                                                        {f.signature && <span className="text-[10px] text-emerald-600 ml-2 font-mono">({f.signature})</span>}
                                                    </div>
                                                </div>
                                            ))}
                                            {diffData?.modified_functions?.map((f: any) => (
                                                <div key={`mf-${f.name}-${f.signature}`} className="bg-amber-50 border border-amber-100 p-4 rounded-xl flex items-center gap-3">
                                                    <div className="w-8 h-8 bg-amber-100 rounded-lg flex items-center justify-center text-amber-600"><Code2 size={16}/></div>
                                                    <div>
                                                        <span className="font-bold text-amber-900 text-sm">Modified Function: {f.name}</span>
                                                        {f.signature && <span className="text-[10px] text-amber-600 ml-2 font-mono">({f.signature})</span>}
                                                    </div>
                                                </div>
                                            ))}

                                            {/* --- Triggers --- */}
                                            {diffData?.added_triggers?.map((t: any) => (
                                                <div key={`at-${t.name}-${t.table}`} className="bg-emerald-50 border border-emerald-100 p-4 rounded-xl flex items-center gap-3">
                                                    <div className="w-8 h-8 bg-emerald-100 rounded-lg flex items-center justify-center text-emerald-600"><Zap size={16}/></div>
                                                    <div>
                                                        <span className="font-bold text-emerald-900 text-sm">New Trigger: {t.name}</span>
                                                        <span className="text-[10px] text-emerald-600 ml-2 font-mono">on {t.table}</span>
                                                    </div>
                                                </div>
                                            ))}
                                            {diffData?.modified_triggers?.map((t: any) => (
                                                <div key={`mt-${t.name}-${t.table}`} className="bg-amber-50 border border-amber-100 p-4 rounded-xl flex items-center gap-3">
                                                    <div className="w-8 h-8 bg-amber-100 rounded-lg flex items-center justify-center text-amber-600"><Zap size={16}/></div>
                                                    <div>
                                                        <span className="font-bold text-amber-900 text-sm">Modified Trigger: {t.name}</span>
                                                        <span className="text-[10px] text-amber-600 ml-2 font-mono">on {t.table}</span>
                                                    </div>
                                                </div>
                                            ))}

                                            {/* --- Empty State (only if truly nothing changed) --- */}
                                            {(!diffData?.added_tables?.length && !diffData?.modified_tables?.length && !diffData?.added_functions?.length && !diffData?.modified_functions?.length && !diffData?.added_triggers?.length && !diffData?.modified_triggers?.length) && (
                                                <p className="text-center text-slate-400 font-bold uppercase text-xs py-20">No schema changes detected.</p>
                                            )}
                                        </div>
                                    )}

                                    {activeTab === 'security' && (
                                        <div className="space-y-3">
                                            {diffData?.policies?.length === 0 && <p className="text-center text-slate-400 font-bold uppercase text-xs py-20">No security policy changes.</p>}
                                            {diffData?.policies?.map((p: any, i: number) => (
                                                <div key={i} className="bg-white border border-slate-200 p-4 rounded-xl flex justify-between items-center">
                                                    <div className="flex items-center gap-3">
                                                        <ShieldCheck size={16} className="text-purple-500"/>
                                                        <div>
                                                            <div className="text-xs font-bold text-slate-800">{p.policy}</div>
                                                            <div className="text-[10px] text-slate-500">on {p.table}</div>
                                                        </div>
                                                    </div>
                                                    <span className="text-[9px] font-black bg-purple-50 text-purple-700 px-2 py-1 rounded">{p.type}</span>
                                                </div>
                                            ))}
                                        </div>
                                    )}

                                    {activeTab === 'sql' && (
                                        <pre className="bg-slate-900 text-emerald-400 p-6 rounded-2xl font-mono text-xs overflow-auto max-h-[500px]">
                                            {diffData?.generated_sql || '-- No SQL generated'}
                                        </pre>
                                    )}
                                </div>
                            </>
                        )}
                    </div>
                )}

                {/* STEP 4: SUCCESS */}
                {step === 'success' && (
                    <div className="h-full flex flex-col items-center justify-center text-center">
                        <div className="w-24 h-24 bg-emerald-100 rounded-full flex items-center justify-center mb-6 animate-in zoom-in"><CheckCircle2 size={48} className="text-emerald-600"/></div>
                        <h2 className="text-3xl font-black text-slate-900 mb-2">Deploy Successful</h2>
                        <p className="text-slate-500 font-medium mb-8">Your changes are now live in production.</p>
                        <button onClick={() => { onSuccess(); onClose(); }} className="bg-slate-900 text-white px-8 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-600 transition-all">Done</button>
                    </div>
                )}
            </div>

            {/* FOOTER ACTIONS */}
            {step !== 'success' && (
                <div className="p-8 border-t border-slate-100 bg-white flex justify-between items-center shrink-0">
                    <button onClick={onClose} className="px-6 py-3 rounded-2xl text-xs font-black text-slate-400 uppercase tracking-widest hover:bg-slate-50">Cancel</button>
                    
                    <div className="flex gap-4">
                        {step === 'diff' && (
                            <button onClick={handleDeploy} disabled={loading} className="px-8 py-3 rounded-2xl font-black text-xs uppercase tracking-widest transition-all flex items-center gap-2 text-white shadow-xl bg-emerald-600 hover:bg-emerald-700">
                                {loading ? <Loader2 className="animate-spin" size={14}/> : <Rocket size={14}/>}
                                Execute Merge
                            </button>
                        )}
                        {step === 'strategy' && (
                            <button onClick={() => setStep('diff')} className="bg-indigo-600 text-white px-8 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-700 transition-all">
                                Analyze Changes
                            </button>
                        )}
                    </div>
                </div>
            )}

        </div>
    </div>
  );
};


export default DeployWizard;
