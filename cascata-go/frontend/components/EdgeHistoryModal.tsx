import React, { useState, useEffect } from 'react';
import { 
    X, History, RotateCcw, GitCompare, Calendar, User, 
    FileText, Clock, Settings, Code, ChevronRight, CheckCircle2,
    ToggleLeft, ToggleRight
} from 'lucide-react';

interface HistoryItem {
    id: string;
    version: number;
    created_at: string;
    created_by: string;
    change_reason: string;
}

interface HistoryDetails {
    id: string;
    name: string;
    content: string;
    runtime: string;
    version: number;
    timeout_ms: number;
    memory_limit_mb: number;
    env_vars: Record<string, string>;
    created_at: string;
    created_by: string;
    change_reason: string;
}

interface EdgeHistoryModalProps {
    isOpen: boolean;
    onClose: () => void;
    projectId: string;
    functionName: string;
    currentCode: string;
    currentTimeoutMs: number;
    currentEnvVars: Record<string, string>;
    currentImports: string[];
    fetchWithAuth: (url: string, options?: any) => Promise<any>;
    onRollbackSuccess: (newCode: string, timeoutMs: number, envVars: Record<string, string>, imports: string[]) => void;
}

// LCS-based diffing algorithm (pure TS, fast and 100% dependency-free)
function computeLineDiff(oldText: string, newText: string) {
    const oldLines = oldText.split('\n');
    const newLines = newText.split('\n');
    
    const dp: number[][] = Array(oldLines.length + 1).fill(null).map(() => Array(newLines.length + 1).fill(0));
    
    for (let i = 1; i <= oldLines.length; i++) {
        for (let j = 1; j <= newLines.length; j++) {
            if (oldLines[i - 1] === newLines[j - 1]) {
                dp[i][j] = dp[i - 1][j - 1] + 1;
            } else {
                dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
            }
        }
    }
    
    const diff: { type: 'added' | 'removed' | 'normal'; text: string }[] = [];
    let i = oldLines.length;
    let j = newLines.length;
    
    while (i > 0 || j > 0) {
        if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
            diff.unshift({ type: 'normal', text: oldLines[i - 1] });
            i--;
            j--;
        } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
            diff.unshift({ type: 'added', text: newLines[j - 1] });
            j--;
        } else {
            diff.unshift({ type: 'removed', text: oldLines[i - 1] });
            i--;
        }
    }
    
    return diff;
}

export const EdgeHistoryModal: React.FC<EdgeHistoryModalProps> = ({
    isOpen,
    onClose,
    projectId,
    functionName,
    currentCode,
    currentTimeoutMs,
    currentEnvVars,
    currentImports,
    fetchWithAuth,
    onRollbackSuccess
}) => {
    const [history, setHistory] = useState<HistoryItem[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [selectedHistoryId, setSelectedHistoryId] = useState<string | null>(null);
    const [selectedDetails, setSelectedDetails] = useState<HistoryDetails | null>(null);
    const [detailsLoading, setDetailsLoading] = useState(false);
    const [diffMode, setDiffMode] = useState<'unified' | 'side-by-side'>('unified');
    const [rollbackConfirm, setRollbackConfirm] = useState(false);
    const [rollingBack, setRollingBack] = useState(false);

    // Fetch history list
    useEffect(() => {
        if (!isOpen) return;
        
        const loadHistory = async () => {
            setLoading(true);
            setError(null);
            try {
                const data = await fetchWithAuth(`/api/data/${projectId}/edge-functions/${functionName}/history`);
                setHistory(data || []);
                if (data && data.length > 0) {
                    // Auto-select most recent historical version
                    setSelectedHistoryId(data[0].id);
                }
            } catch (err: any) {
                setError(err.message || 'Failed to fetch history versions');
            } finally {
                setLoading(false);
            }
        };

        loadHistory();
    }, [isOpen, projectId, functionName]);

    // Fetch single history details
    useEffect(() => {
        if (!selectedHistoryId) return;

        const loadDetails = async () => {
            setDetailsLoading(true);
            try {
                const details = await fetchWithAuth(`/api/data/${projectId}/edge-functions/${functionName}/history/${selectedHistoryId}`);
                setSelectedDetails(details);
            } catch (err: any) {
                console.error('Error fetching version details:', err);
            } finally {
                setDetailsLoading(false);
            }
        };

        loadDetails();
    }, [selectedHistoryId, projectId, functionName]);

    if (!isOpen) return null;

    const handleRollback = async () => {
        if (!selectedDetails) return;
        setRollingBack(true);
        try {
            await fetchWithAuth(`/api/data/${projectId}/edge-functions/${functionName}/rollback/${selectedDetails.id}`, {
                method: 'POST'
            });
            // Notify success
            onRollbackSuccess(
                selectedDetails.content, 
                selectedDetails.timeout_ms, 
                selectedDetails.env_vars || {},
                selectedDetails.metadata?.imports || []
            );
            setRollbackConfirm(false);
            onClose();
        } catch (err: any) {
            alert(err.message || 'Failed to execute rollback');
        } finally {
            setRollingBack(false);
        }
    };

    // Calculate Diff between Selected History and Current Editor Code
    const diffLines = selectedDetails ? computeLineDiff(selectedDetails.content, currentCode) : [];

    return (
        <div className="fixed inset-0 bg-slate-900/60 backdrop-blur-sm z-50 flex items-center justify-center p-4 overflow-hidden animate-in fade-in duration-200">
            <div className="bg-white border border-slate-200 w-full max-w-7xl h-[90vh] rounded-2xl shadow-2xl flex flex-col overflow-hidden animate-in slide-in-from-bottom-8 duration-300">
                {/* Modal Header */}
                <header className="px-6 py-4 border-b border-slate-200 flex items-center justify-between bg-slate-50 shrink-0">
                    <div className="flex items-center gap-3">
                        <div className="p-2 bg-indigo-50 text-indigo-600 rounded-xl">
                            <History size={20} />
                        </div>
                        <div>
                            <h2 className="text-base font-bold text-slate-800">Version History & Visual Comparison</h2>
                            <p className="text-xs text-slate-500 font-mono">edge-function: {functionName}</p>
                        </div>
                    </div>
                    <div className="flex items-center gap-4">
                        {/* Diff Mode Toggle */}
                        <div className="flex bg-slate-200/60 p-1 rounded-lg">
                            <button 
                                onClick={() => setDiffMode('unified')} 
                                className={`px-3 py-1 text-xs font-bold rounded-md transition-all ${diffMode === 'unified' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-500 hover:text-slate-700'}`}
                            >
                                Unified
                            </button>
                            <button 
                                onClick={() => setDiffMode('side-by-side')} 
                                className={`px-3 py-1 text-xs font-bold rounded-md transition-all ${diffMode === 'side-by-side' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-500 hover:text-slate-700'}`}
                            >
                                Side-by-Side
                            </button>
                        </div>
                        <button onClick={onClose} className="p-1.5 text-slate-400 hover:text-slate-600 hover:bg-slate-100 rounded-lg transition-all">
                            <X size={20} />
                        </button>
                    </div>
                </header>

                {/* Modal Body */}
                <div className="flex-1 flex overflow-hidden min-h-0">
                    {/* Left Sidebar: Version List */}
                    <div className="w-80 border-r border-slate-200 flex flex-col overflow-hidden bg-slate-50/50 shrink-0">
                        <div className="p-4 border-b border-slate-200 bg-white">
                            <div className="text-xs font-bold text-slate-400 uppercase tracking-wider mb-1">Select Version</div>
                            <div className="text-[10px] text-slate-500">Compare historical snapshots and restore as active code.</div>
                        </div>
                        <div className="flex-1 overflow-y-auto p-3 space-y-2">
                            {loading ? (
                                <div className="flex flex-col items-center justify-center py-12 text-slate-400 gap-2">
                                    <Clock size={24} className="animate-spin text-indigo-500" />
                                    <span className="text-xs font-medium">Loading version list...</span>
                                </div>
                            ) : error ? (
                                <div className="p-4 bg-rose-50 border border-rose-100 rounded-xl text-xs text-rose-600 font-medium">
                                    {error}
                                </div>
                            ) : history.length === 0 ? (
                                <div className="text-center py-12 text-xs text-slate-400 italic">
                                    No historical versions found for this function.
                                </div>
                            ) : (
                                history.map((item) => {
                                    const isSelected = selectedHistoryId === item.id;
                                    const dateStr = new Date(item.created_at).toLocaleString();
                                    return (
                                        <div
                                            key={item.id}
                                            onClick={() => setSelectedHistoryId(item.id)}
                                            className={`p-3.5 rounded-xl border transition-all cursor-pointer ${
                                                isSelected 
                                                    ? 'bg-indigo-600 border-indigo-600 text-white shadow-md shadow-indigo-100' 
                                                    : 'bg-white border-slate-200 hover:border-slate-300 text-slate-700 hover:bg-slate-50/70'
                                            }`}
                                        >
                                            <div className="flex items-center justify-between mb-1.5">
                                                <span className={`text-xs font-mono font-bold px-2 py-0.5 rounded-full ${isSelected ? 'bg-indigo-500/60 text-white' : 'bg-slate-100 text-slate-600'}`}>
                                                    v{item.version}
                                                </span>
                                                <span className={`text-[10px] font-medium opacity-80 flex items-center gap-1`}>
                                                    <Calendar size={10} />
                                                    {dateStr}
                                                </span>
                                            </div>
                                            <div className="text-xs font-semibold line-clamp-2 leading-relaxed mb-2 opacity-90">
                                                {item.change_reason || 'Manual Update'}
                                            </div>
                                            <div className="flex items-center gap-1.5 text-[10px] opacity-75 font-medium">
                                                <User size={10} />
                                                <span className="truncate">By: {item.created_by || 'system_role'}</span>
                                            </div>
                                        </div>
                                    );
                                })
                            )}
                        </div>
                    </div>

                    {/* Right Content Pane: Diff & Metadata Comparison */}
                    <div className="flex-1 flex flex-col overflow-hidden min-w-0 bg-slate-50">
                        {detailsLoading ? (
                            <div className="flex-1 flex flex-col items-center justify-center text-slate-400 gap-2">
                                <History size={32} className="animate-spin text-indigo-500" />
                                <span className="text-sm font-semibold">Decrypting historical version...</span>
                            </div>
                        ) : !selectedDetails ? (
                            <div className="flex-1 flex flex-col items-center justify-center text-slate-400 italic text-sm">
                                Select a version from the left panel to compare code.
                            </div>
                        ) : (
                            <div className="flex-1 flex flex-col overflow-hidden min-h-0">
                                {/* Metadata Comparison Bar */}
                                <div className="grid grid-cols-3 border-b border-slate-200 bg-white shrink-0 text-slate-700 text-xs font-semibold">
                                    {/* Timeout */}
                                    <div className="p-4 border-r border-slate-200 flex items-center justify-between">
                                        <div className="flex items-center gap-2">
                                            <Clock size={16} className="text-slate-400" />
                                            <span>Timeout Limit</span>
                                        </div>
                                        <div className="flex items-center gap-2 font-mono">
                                            <span className="text-rose-500 line-through bg-rose-50 px-1.5 py-0.5 rounded">{selectedDetails.timeout_ms}ms</span>
                                            <ChevronRight size={12} className="text-slate-400" />
                                            <span className="text-emerald-600 bg-emerald-50 px-1.5 py-0.5 rounded">{currentTimeoutMs}ms</span>
                                        </div>
                                    </div>
                                    {/* Imports count */}
                                    <div className="p-4 border-r border-slate-200 flex items-center justify-between">
                                        <div className="flex items-center gap-2">
                                            <Settings size={16} className="text-slate-400" />
                                            <span>Imports Count</span>
                                        </div>
                                        <div className="flex items-center gap-2 font-mono">
                                            <span className="bg-slate-100 text-slate-600 px-1.5 py-0.5 rounded">{(selectedDetails.metadata?.imports || []).length} imports</span>
                                            <ChevronRight size={12} className="text-slate-400" />
                                            <span className="bg-indigo-50 text-indigo-600 px-1.5 py-0.5 rounded">{currentImports.length} imports</span>
                                        </div>
                                    </div>
                                    {/* Action Bar */}
                                    <div className="p-3 flex items-center justify-end bg-slate-50/50">
                                        {rollbackConfirm ? (
                                            <div className="flex items-center gap-2">
                                                <span className="text-[10px] text-rose-600 font-bold animate-pulse">Are you sure?</span>
                                                <button 
                                                    onClick={handleRollback}
                                                    disabled={rollingBack}
                                                    className="px-3 py-1.5 bg-rose-600 text-white font-bold rounded-lg text-xs hover:bg-rose-700 shadow-sm flex items-center gap-1.5"
                                                >
                                                    {rollingBack ? <Clock size={12} className="animate-spin" /> : <RotateCcw size={12} />} Yes, Rollback
                                                </button>
                                                <button 
                                                    onClick={() => setRollbackConfirm(false)}
                                                    className="px-3 py-1.5 bg-white border border-slate-200 text-slate-600 font-bold rounded-lg text-xs hover:bg-slate-50"
                                                >
                                                    Cancel
                                                </button>
                                            </div>
                                        ) : (
                                            <button 
                                                onClick={() => setRollbackConfirm(true)}
                                                className="px-4 py-2 bg-indigo-600 text-white font-bold rounded-xl text-xs hover:bg-indigo-700 shadow-md shadow-indigo-100 flex items-center gap-2 transition-all"
                                            >
                                                <RotateCcw size={14} /> Restore Version (Rollback v{selectedDetails.version})
                                            </button>
                                        )}
                                    </div>
                                </div>

                                {/* Environment Variables Comparison Panel */}
                                <div className="px-6 py-3 bg-white border-b border-slate-100 flex flex-wrap gap-4 shrink-0 text-xs">
                                    <div className="font-bold text-slate-400 uppercase tracking-wider flex items-center gap-1 w-full mb-1">
                                        <Settings size={14} /> Environment Variables Differences
                                    </div>
                                    {/* Env list keys compared */}
                                    {(() => {
                                        const histEnv = selectedDetails.env_vars || {};
                                        const currEnv = currentEnvVars || {};
                                        const allKeys = Array.from(new Set([...Object.keys(histEnv), ...Object.keys(currEnv)]));
                                        
                                        if (allKeys.length === 0) {
                                            return <span className="text-slate-400 italic text-[11px]">No environment variables defined.</span>;
                                        }

                                        return allKeys.map(k => {
                                            const histVal = histEnv[k];
                                            const currVal = currEnv[k];
                                            
                                            if (histVal === currVal) {
                                                return (
                                                    <span key={k} className="bg-slate-50 text-slate-600 px-2 py-1 rounded-lg border border-slate-200 font-mono text-[10px]">
                                                        {k}: <span className="font-bold">Unchanged</span>
                                                    </span>
                                                );
                                            } else if (histVal && !currVal) {
                                                return (
                                                    <span key={k} className="bg-rose-50 border border-rose-100 text-rose-700 px-2 py-1 rounded-lg font-mono text-[10px]">
                                                        -{k} (Removed in current)
                                                    </span>
                                                );
                                            } else if (!histVal && currVal) {
                                                return (
                                                    <span key={k} className="bg-emerald-50 border border-emerald-100 text-emerald-700 px-2 py-1 rounded-lg font-mono text-[10px]">
                                                        +{k} (Added in current)
                                                    </span>
                                                );
                                            } else {
                                                return (
                                                    <span key={k} className="bg-amber-50 border border-amber-100 text-amber-700 px-2 py-1 rounded-lg font-mono text-[10px]">
                                                        ~{k} (Modified)
                                                    </span>
                                                );
                                            }
                                        });
                                    })()}
                                </div>

                                {/* Code Diff Workspace */}
                                <div className="flex-1 overflow-auto p-6">
                                    <div className="border border-slate-200 rounded-2xl overflow-hidden bg-[#1e1e1e] h-full flex flex-col shadow-inner">
                                        <div className="px-4 py-2.5 bg-slate-800 border-b border-slate-700 text-slate-400 text-xs font-mono flex items-center justify-between shrink-0">
                                            <div className="flex items-center gap-2">
                                                <Code size={14} className="text-indigo-400" />
                                                <span>Source Code Diff</span>
                                            </div>
                                            <div className="flex items-center gap-4 text-[10px]">
                                                <span className="flex items-center gap-1.5"><span className="w-2.5 h-2.5 bg-rose-950/80 border border-rose-500/40 rounded"></span> Deleted in Editor</span>
                                                <span className="flex items-center gap-1.5"><span className="w-2.5 h-2.5 bg-emerald-950/80 border border-emerald-500/40 rounded"></span> Added in Editor</span>
                                            </div>
                                        </div>

                                        <div className="flex-1 overflow-auto p-4 font-mono text-[13px] leading-relaxed text-slate-300">
                                            {diffMode === 'unified' ? (
                                                <div className="w-full min-w-max space-y-0.5">
                                                    {diffLines.map((line, idx) => {
                                                        const isAdded = line.type === 'added';
                                                        const isRemoved = line.type === 'removed';
                                                        
                                                        const bgClass = isAdded 
                                                            ? 'bg-emerald-950/40 border-l-2 border-emerald-500 text-emerald-300 px-2' 
                                                            : isRemoved 
                                                                ? 'bg-rose-950/40 border-l-2 border-rose-500 text-rose-300 px-2' 
                                                                : 'px-2 border-l-2 border-transparent';

                                                        return (
                                                            <div key={idx} className={`flex items-start py-0.5 rounded font-mono ${bgClass}`}>
                                                                <span className="w-8 shrink-0 text-slate-600 text-right select-none pr-3 text-[11px] font-bold">
                                                                    {isAdded ? '+' : isRemoved ? '-' : idx + 1}
                                                                </span>
                                                                <pre className="whitespace-pre-wrap font-mono break-all">{line.text || ' '}</pre>
                                                            </div>
                                                        );
                                                    })}
                                                </div>
                                            ) : (
                                                /* Side-by-Side View */
                                                <div className="grid grid-cols-2 gap-4 h-full min-w-[900px]">
                                                    {/* Historical Version */}
                                                    <div className="border border-slate-700/60 rounded-xl p-3 bg-slate-900/50 overflow-auto">
                                                        <div className="text-[10px] font-bold text-indigo-400 uppercase tracking-wider mb-2 border-b border-slate-800 pb-1">
                                                            Historical Version (v{selectedDetails.version})
                                                        </div>
                                                        <pre className="font-mono text-xs whitespace-pre select-all text-slate-400 leading-normal">
                                                            {selectedDetails.content}
                                                        </pre>
                                                    </div>
                                                    {/* Current Editor Version */}
                                                    <div className="border border-slate-700/60 rounded-xl p-3 bg-slate-900/50 overflow-auto">
                                                        <div className="text-[10px] font-bold text-emerald-400 uppercase tracking-wider mb-2 border-b border-slate-800 pb-1">
                                                            Current Code in Editor
                                                        </div>
                                                        <pre className="font-mono text-xs whitespace-pre select-all text-slate-300 leading-normal">
                                                            {currentCode || '// Empty function body'}
                                                        </pre>
                                                    </div>
                                                </div>
                                            )}
                                        </div>
                                    </div>
                                </div>
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </div>
    );
};
