import React from 'react';
import { GitBranch, Clock, Database, Loader2, Plus, RefreshCw, Trash2, FolderOpen, Shield, X } from 'lucide-react';

export interface BranchRecord {
  id: string;
  name: string;
  branch_type: string;
  status: string;
  is_main: boolean;
  parent_branch?: string | null;
  created_at: string;
  data_mode?: string | null;
  expires_at?: string | null;
}

interface BranchManagerModalProps {
  isOpen: boolean;
  onClose: () => void;
  selectedProjectId: string;
  currentEnv: string;
  branches: BranchRecord[];
  branchesLoading: boolean;
  loadBranches: (projectId: string) => Promise<void>;
  
  // Handlers
  handleCreateBranch: () => Promise<void>;
  handleCreateDataBranch: () => Promise<void>;
  handleDeleteBranch: (branchName: string) => Promise<void>;
  handleCreateBranchFromSnapshot: (snapshotName: string) => Promise<void>;
  handleRestoreDeploy: (deployId: string) => Promise<void>;
  
  // Forms & errors
  branchFormName: string;
  setBranchFormName: (val: string) => void;
  branchError: string | null;
  
  branchTab: 'branches' | 'history';
  setBranchTab: (val: 'branches' | 'history') => void;
  branchParentName: string;
  setBranchParentName: (val: string) => void;
  
  // History & rollback
  deploysHistory: any[];
  loadDeploysHistory: (projectId: string) => Promise<void>;
  deploysLoading: boolean;
  restoringId: string | null;
  
  // Data branch options
  dataFormName: string;
  setDataFormName: (val: string) => void;
  dataFormMode: 'copy' | 'reflective' | 'schema_only';
  setDataFormMode: (val: 'copy' | 'reflective' | 'schema_only') => void;
  dataFormTTL: number;
  setDataFormTTL: (val: number) => void;

  setShowDeployWizard: (val: boolean) => void;
  currentEnvSetter: (env: string) => void;
  currentHash: string;
}

export const BranchManagerModal: React.FC<BranchManagerModalProps> = ({
  isOpen,
  onClose,
  selectedProjectId,
  currentEnv,
  branches,
  branchesLoading,
  loadBranches,
  handleCreateBranch,
  handleCreateDataBranch,
  handleDeleteBranch,
  handleCreateBranchFromSnapshot,
  handleRestoreDeploy,
  branchFormName,
  setBranchFormName,
  branchError,
  branchTab,
  setBranchTab,
  branchParentName,
  setBranchParentName,
  deploysHistory,
  loadDeploysHistory,
  deploysLoading,
  restoringId,
  dataFormName,
  setDataFormName,
  dataFormMode,
  setDataFormMode,
  dataFormTTL,
  setDataFormTTL,
  setShowDeployWizard,
  currentEnvSetter,
  currentHash,
}) => {
  if (!isOpen || !selectedProjectId) return null;

  return (
    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[900] flex items-center justify-center p-8 transition-all duration-300 animate-in fade-in">
      <div className="bg-white rounded-[2.5rem] w-full max-w-4xl max-h-[85vh] overflow-hidden shadow-2xl border border-slate-200 flex flex-col animate-slide-up">
        <div className="p-8 border-b border-slate-100 flex items-center justify-between bg-slate-50/50">
          <div className="flex items-center gap-4">
            <div className="w-14 h-14 rounded-2xl bg-emerald-600 text-white flex items-center justify-center shadow-lg">
              <GitBranch size={24} />
            </div>
            <div>
              <h3 className="text-2xl font-black text-slate-900 tracking-tight">Branch Manager</h3>
              <p className="text-[10px] font-bold uppercase tracking-widest text-slate-400 mt-1">Sovereign control & safety environments</p>
            </div>
          </div>
          <button onClick={onClose} className="p-3 hover:bg-slate-200 rounded-full text-slate-400"><X size={20} /></button>
        </div>

        {/* Premium Tab Bar */}
        <div className="flex border-b border-slate-100 bg-slate-50/20 px-8 py-2 gap-4">
          <button
            onClick={() => setBranchTab('branches')}
            className={`flex items-center gap-2 py-3 px-4 font-black text-xs uppercase tracking-wider transition-all border-b-2 ${
              branchTab === 'branches'
                ? 'border-emerald-600 text-emerald-600'
                : 'border-transparent text-slate-400 hover:text-slate-600'
            }`}
          >
            <GitBranch size={14} />
            Environment Branches
          </button>
          <button
            onClick={async () => {
              setBranchTab('history');
              await loadDeploysHistory(selectedProjectId);
            }}
            className={`flex items-center gap-2 py-3 px-4 font-black text-xs uppercase tracking-wider transition-all border-b-2 ${
              branchTab === 'history'
                ? 'border-emerald-600 text-emerald-600'
                : 'border-transparent text-slate-400 hover:text-slate-600'
            }`}
          >
            <Clock size={14} />
            Merge & Checkpoint History
          </button>
        </div>

        <div className="p-8 overflow-y-auto space-y-8 flex-1">
          {branchTab === 'branches' ? (
            <>
              {/* New Branch Form */}
              <div className="bg-slate-50 border border-slate-200 rounded-[2rem] p-6">
                <div className="mb-4">
                  <h4 className="text-lg font-black text-slate-900">New Environment Branch</h4>
                  <p className="text-xs text-slate-500 font-medium mt-1">Branches allow concurrent and safe schema development, isolated from production.</p>
                </div>
                
                <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                  <div className="md:col-span-2 flex flex-col gap-1">
                    <label className="text-[9px] font-black uppercase tracking-wider text-slate-400 pl-1">Branch Name</label>
                    <input
                      value={branchFormName}
                      onChange={(e) => {
                        const raw = e.target.value;
                        const clean = raw
                          .normalize("NFD")
                          .replace(/[\u0300-\u036f]/g, "")
                          .replace(/\s+/g, "-")
                          .replace(/[^a-zA-Z0-9-/_]/g, "")
                          .toLowerCase();
                        setBranchFormName(clean);
                      }}
                      placeholder="feat/new-checkout"
                      className="w-full rounded-2xl border border-slate-200 bg-white px-4 py-3.5 text-sm font-bold text-slate-800 outline-none focus:border-emerald-500 transition-colors"
                    />
                  </div>
                  
                  <div className="flex flex-col gap-1">
                    <label className="text-[9px] font-black uppercase tracking-wider text-slate-400 pl-1">Parent Branch (Origin)</label>
                    <select
                      value={branchParentName}
                      onChange={(e) => setBranchParentName(e.target.value)}
                      className="w-full rounded-2xl border border-slate-200 bg-white px-4 py-3.5 text-sm font-bold text-slate-800 outline-none focus:border-emerald-500 transition-colors"
                    >
                      <option value="main">main (Production)</option>
                      {branches.filter(b => b.branch_type === 'environment' && !b.is_main).map(b => (
                        <option key={b.id} value={b.name}>{b.name}</option>
                      ))}
                    </select>
                  </div>
                </div>

                <div className="flex justify-end mt-4">
                  <button
                    onClick={handleCreateBranch}
                    disabled={branchesLoading || !branchFormName.trim()}
                    className="bg-emerald-600 text-white px-6 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-emerald-700 transition-all disabled:opacity-50 flex items-center gap-2 active:scale-95 shadow-sm hover:shadow"
                  >
                    {branchesLoading ? <Loader2 size={14} className="animate-spin" /> : <Plus size={14} />}
                    Create Branch
                  </button>
                </div>
              </div>

              {/* New Data Sandbox Branch Form */}
              <div className="bg-slate-50 border border-slate-200 rounded-[2rem] p-6 mt-4">
                <div className="mb-4">
                  <h4 className="text-lg font-black text-slate-900 flex items-center gap-2">
                    <Database size={16} className="text-indigo-600" /> New Data Sandbox Branch
                  </h4>
                  <p className="text-xs text-slate-500 font-medium mt-1">
                    Create a dynamic testing sandbox branch populated with a data copy, schema-only structure, or a zero-copy reflective FDW bridge.
                  </p>
                </div>
                
                <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                  <div className="flex flex-col gap-1">
                    <label className="text-[9px] font-black uppercase tracking-wider text-slate-400 pl-1">Branch Name</label>
                    <input
                      value={dataFormName}
                      onChange={(e) => {
                        const raw = e.target.value;
                        const clean = raw
                          .normalize("NFD")
                          .replace(/[\u0300-\u036f]/g, "")
                          .replace(/\s+/g, "-")
                          .replace(/[^a-zA-Z0-9-/_]/g, "")
                          .toLowerCase();
                        setDataFormName(clean);
                      }}
                      placeholder="dev-testing"
                      className="w-full rounded-2xl border border-slate-200 bg-white px-4 py-3.5 text-sm font-bold text-slate-800 outline-none focus:border-indigo-500 transition-colors"
                    />
                  </div>
                  
                  <div className="flex flex-col gap-1">
                    <label className="text-[9px] font-black uppercase tracking-wider text-slate-400 pl-1">Data Mode</label>
                    <select
                      value={dataFormMode}
                      onChange={(e) => setDataFormMode(e.target.value as any)}
                      className="w-full rounded-2xl border border-slate-200 bg-white px-4 py-3.5 text-sm font-bold text-slate-800 outline-none focus:border-indigo-500 transition-colors"
                    >
                      <option value="copy">Full Copy (100% clone)</option>
                      <option value="reflective">Reflective (FDW - Zero Copy)</option>
                      <option value="schema_only">Schema Only (Truncated data)</option>
                    </select>
                  </div>

                  <div className="flex flex-col gap-1">
                    <label className="text-[9px] font-black uppercase tracking-wider text-slate-400 pl-1">TTL (Expiration)</label>
                    <select
                      value={dataFormTTL}
                      onChange={(e) => setDataFormTTL(Number(e.target.value))}
                      className="w-full rounded-2xl border border-slate-200 bg-white px-4 py-3.5 text-sm font-bold text-slate-800 outline-none focus:border-indigo-500 transition-colors"
                    >
                      <option value={24}>24 Hours (1 Day)</option>
                      <option value={72}>72 Hours (3 Days)</option>
                      <option value={168}>168 Hours (7 Days)</option>
                      <option value={720}>720 Hours (30 Days)</option>
                    </select>
                  </div>
                </div>

                <div className="flex justify-end mt-4">
                  <button
                    onClick={handleCreateDataBranch}
                    disabled={branchesLoading || !dataFormName.trim()}
                    className="bg-indigo-600 text-white px-6 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-700 transition-all disabled:opacity-50 flex items-center gap-2 active:scale-95 shadow-sm hover:shadow"
                  >
                    {branchesLoading ? <Loader2 size={14} className="animate-spin" /> : <Plus size={14} />}
                    Create Data Branch
                  </button>
                </div>
              </div>

              {/* Branches List */}
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <h4 className="text-lg font-black text-slate-900">Active Environments</h4>
                  <button
                    onClick={() => selectedProjectId && loadBranches(selectedProjectId)}
                    className="text-xs font-black uppercase tracking-widest text-slate-500 hover:text-slate-700 flex items-center gap-1"
                  >
                    <RefreshCw size={12} className={branchesLoading ? 'animate-spin' : ''} />
                    Refresh
                  </button>
                </div>

                {branchError && (
                  <div className="bg-rose-50 border border-rose-100 text-rose-700 text-sm font-medium rounded-2xl p-4">
                    {branchError}
                  </div>
                )}

                <div className="space-y-3">
                  {branchesLoading && (
                    <div className="py-12 flex justify-center">
                      <Loader2 className="animate-spin text-emerald-600" size={28} />
                    </div>
                  )}

                  {!branchesLoading && branches.filter((branch) => branch.branch_type === 'environment').map((branch) => (
                    <div key={branch.id} className="bg-white border border-slate-200 rounded-2xl p-5 flex items-center justify-between gap-4 hover:border-slate-300 transition-all shadow-sm">
                      <div className="min-w-0">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="text-sm font-black text-slate-900">{branch.name}</span>
                          {branch.is_main && (
                            <span className="text-[10px] font-black uppercase tracking-widest px-2 py-0.5 rounded bg-slate-100 text-slate-600">
                              main
                            </span>
                          )}
                          {!branch.is_main && (
                            <span className="text-[10px] font-black uppercase tracking-widest px-2 py-0.5 rounded bg-emerald-50 text-emerald-700">
                              {branch.status}
                            </span>
                          )}
                        </div>
                        <div className="text-[11px] text-slate-500 font-medium mt-2 flex items-center gap-2 flex-wrap">
                          <span className="bg-slate-50 px-2 py-0.5 rounded border border-slate-100 font-bold">
                            Parent: {branch.parent_branch || 'root'}
                          </span>
                          <span>·</span>
                          <span>Created: {new Date(branch.created_at).toLocaleString()}</span>
                        </div>
                      </div>

                      <div className="flex items-center gap-2 shrink-0">
                        {!branch.is_main && (
                          <>
                            <button
                              onClick={async () => {
                                if (!selectedProjectId) return;
                                try {
                                  const res = await fetch(`/api/data/${selectedProjectId}/branch/access`, {
                                    method: 'POST',
                                    headers: {
                                      'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`,
                                      'Content-Type': 'application/json'
                                    },
                                    body: JSON.stringify({ branch_name: branch.name })
                                  });
                                  const data = await res.json();
                                  if (!res.ok) throw new Error(data?.error || 'Failed to access branch.');

                                  localStorage.setItem('cascata_env', data.env_identifier);
                                  currentEnvSetter(data.env_identifier);
                                  onClose();

                                  const parts = currentHash.split('/');
                                  let currentSection = 'overview';
                                  if (parts[1] === 'project' && parts[3] && parts[3] !== 'branch') {
                                    currentSection = parts[3];
                                  }
                                  
                                  const newHash = `#/project/${selectedProjectId}/${currentSection}/branch/${data.env_identifier}`;
                                  window.location.replace(newHash);
                                  window.location.reload();
                                } catch (e: any) {
                                  alert(`Failed to access branch: ${e?.message}`);
                                }
                              }}
                              className={`${currentEnv === branch.name ? 'bg-emerald-600 hover:bg-emerald-700' : 'bg-blue-600 hover:bg-blue-700'} text-white px-4 py-2.5 rounded-xl font-black text-[10px] uppercase tracking-widest transition-all flex items-center gap-1.5 active:scale-95`}
                            >
                              <Database size={12} />
                              {currentEnv === branch.name ? 'Active' : 'Access'}
                            </button>
                            <button
                              onClick={() => {
                                onClose();
                                setShowDeployWizard(true);
                              }}
                              className="bg-amber-500 hover:bg-amber-600 text-white px-4 py-2.5 rounded-xl font-black text-[10px] uppercase tracking-widest transition-all active:scale-95"
                            >
                              Merge
                            </button>
                            <button
                              onClick={() => handleDeleteBranch(branch.name)}
                              className="bg-rose-50 hover:bg-rose-100 text-rose-600 px-4 py-2.5 rounded-xl font-black text-[10px] uppercase tracking-widest transition-all active:scale-95"
                            >
                              Delete
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              {/* Data Sandbox Branches List */}
              <div className="space-y-4 pt-6 border-t border-slate-100">
                <div className="flex items-center justify-between">
                  <h4 className="text-lg font-black text-slate-900 flex items-center gap-2">
                    <Database size={16} className="text-indigo-600" /> Active Sandbox Databases
                  </h4>
                </div>

                <div className="space-y-3">
                  {branches.filter((branch) => branch.branch_type === 'data').length === 0 ? (
                    <div className="py-8 text-center bg-slate-50 border border-slate-100 rounded-2xl p-4 text-xs text-slate-400 font-medium">
                      No active data sandboxes. Create one above to start testing with a real-time bridge or clone.
                    </div>
                  ) : (
                    branches.filter((branch) => branch.branch_type === 'data').map((branch) => (
                      <div key={branch.id} className="bg-white border border-indigo-100 rounded-2xl p-5 flex items-center justify-between gap-4 hover:border-indigo-200 transition-all shadow-sm">
                        <div className="min-w-0">
                          <div className="flex items-center gap-2 flex-wrap">
                            <span className="text-sm font-black text-slate-900">{branch.name}</span>
                            <span className={`text-[10px] font-black uppercase tracking-widest px-2 py-0.5 rounded ${
                              branch.data_mode === 'reflective' 
                                ? 'bg-blue-50 text-blue-700 border border-blue-100' 
                                : branch.data_mode === 'schema_only'
                                  ? 'bg-amber-50 text-amber-700 border border-amber-100'
                                  : 'bg-indigo-50 text-indigo-700 border border-indigo-100'
                            }`}>
                              {branch.data_mode === 'reflective' ? 'Reflective FDW' : branch.data_mode === 'schema_only' ? 'Schema Only' : 'Full Copy'}
                            </span>
                          </div>
                          <div className="text-[11px] text-slate-500 font-medium mt-2 flex items-center gap-2 flex-wrap">
                            <span className="bg-slate-50 px-2 py-0.5 rounded border border-slate-100 font-bold text-[10px]">
                              Expires: {branch.expires_at ? new Date(branch.expires_at).toLocaleString() : 'N/A'}
                            </span>
                            <span>·</span>
                            <span>Created: {new Date(branch.created_at).toLocaleString()}</span>
                          </div>
                        </div>

                        <div className="flex items-center gap-2 shrink-0">
                          <button
                            onClick={async () => {
                              if (!selectedProjectId) return;
                              try {
                                const res = await fetch(`/api/data/${selectedProjectId}/branch/access`, {
                                  method: 'POST',
                                  headers: {
                                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`,
                                    'Content-Type': 'application/json'
                                  },
                                  body: JSON.stringify({ branch_name: branch.name })
                                });
                                const data = await res.json();
                                if (!res.ok) throw new Error(data?.error || 'Failed to access branch.');

                                localStorage.setItem('cascata_env', data.env_identifier);
                                currentEnvSetter(data.env_identifier);
                                onClose();

                                const parts = currentHash.split('/');
                                let currentSection = 'overview';
                                if (parts[1] === 'project' && parts[3] && parts[3] !== 'branch') {
                                  currentSection = parts[3];
                                }
                                
                                const newHash = `#/project/${selectedProjectId}/${currentSection}/branch/${data.env_identifier}`;
                                window.location.replace(newHash);
                                window.location.reload();
                              } catch (e: any) {
                                alert(`Failed to access branch: ${e?.message}`);
                              }
                            }}
                            className={`${currentEnv === branch.name ? 'bg-emerald-600 hover:bg-emerald-700' : 'bg-indigo-600 hover:bg-indigo-700'} text-white px-4 py-2.5 rounded-xl font-black text-[10px] uppercase tracking-widest transition-all flex items-center gap-1.5 active:scale-95`}
                          >
                            <Database size={12} />
                            {currentEnv === branch.name ? 'Active' : 'Access'}
                          </button>
                          <button
                            onClick={() => handleDeleteBranch(branch.name)}
                            className="bg-rose-50 hover:bg-rose-100 text-rose-600 px-4 py-2.5 rounded-xl font-black text-[10px] uppercase tracking-widest transition-all active:scale-95"
                          >
                            Delete
                          </button>
                        </div>
                      </div>
                    ))
                  )}
                </div>
              </div>
            </>
          ) : (
            /* History / Rollback Log Aba */
            <div className="space-y-6">
              <div className="flex items-center justify-between">
                <div>
                  <h4 className="text-lg font-black text-slate-900">Merge & Checkpoint Timeline</h4>
                  <p className="text-xs text-slate-500 font-medium mt-1">Track all safe merges and manual schema snapshots</p>
                </div>
                <button
                  onClick={() => selectedProjectId && loadDeploysHistory(selectedProjectId)}
                  className="text-xs font-black uppercase tracking-widest text-slate-500 hover:text-slate-700 flex items-center gap-1"
                >
                  <RefreshCw size={12} className={deploysLoading ? 'animate-spin' : ''} />
                  Refresh History
                </button>
              </div>

              {deploysLoading ? (
                <div className="py-16 flex justify-center">
                  <Loader2 className="animate-spin text-emerald-600" size={28} />
                </div>
              ) : deploysHistory.length === 0 ? (
                <div className="py-16 text-center bg-slate-50 border border-slate-100 rounded-3xl p-8">
                  <Clock className="mx-auto text-slate-300 mb-3" size={36} />
                  <h5 className="font-black text-slate-600 text-sm">No merges or deploys recorded</h5>
                  <p className="text-xs text-slate-400 mt-1 max-w-sm mx-auto">Toda vez que você executa um merge ou deploy, ele aparecerá aqui com um snapshot de segurança.</p>
                </div>
              ) : (
                <div className="relative pl-6 border-l-2 border-slate-100 space-y-8">
                  {deploysHistory.map((deploy) => (
                    <div key={deploy.id} className="relative">
                      {/* Timeline Dot */}
                      <div className={`absolute -left-[31px] top-1.5 w-4 h-4 rounded-full border-4 border-white shadow ${
                        deploy.status === 'success' ? 'bg-emerald-500' :
                        deploy.status === 'failed' ? 'bg-rose-500' : 'bg-amber-500'
                      }`} />
                      
                      <div className="bg-white border border-slate-100 hover:border-slate-200 rounded-[1.5rem] p-6 shadow-sm hover:shadow transition-all">
                        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                          <div>
                            <div className="flex items-center gap-2 flex-wrap">
                              <span className={`text-[9px] font-black uppercase tracking-widest px-2.5 py-1 rounded-full ${
                                deploy.status === 'success' ? 'bg-emerald-50 text-emerald-700 border border-emerald-100' :
                                deploy.status === 'failed' ? 'bg-rose-50 text-rose-700 border border-rose-100' :
                                'bg-amber-50 text-amber-700 border border-amber-100'
                              }`}>
                                {deploy.status}
                              </span>
                              <span className="text-xs font-black text-slate-800">
                                {deploy.source_branch} <span className="text-slate-400 font-bold">→</span> {deploy.target_branch}
                              </span>
                            </div>
                            
                            <div className="text-[11px] text-slate-400 mt-2 font-medium">
                              Started: {new Date(deploy.started_at).toLocaleString()}
                              {deploy.duration_ms && ` · Duration: ${deploy.duration_ms}ms`}
                            </div>

                            {deploy.error_message && (
                              <div className="mt-3 text-xs bg-rose-50 text-rose-700 p-3 rounded-xl border border-rose-100 font-mono break-all">
                                {deploy.error_message}
                              </div>
                            )}
                          </div>

                          <div className="flex flex-col md:items-end gap-2 text-left md:text-right shrink-0">
                            {deploy.snapshot_name && (
                              <div className="flex items-center gap-2 bg-slate-50 border border-slate-100 px-3.5 py-2 rounded-2xl text-[10px] font-black uppercase tracking-widest text-slate-600 shadow-sm">
                                <Shield size={12} className="text-emerald-600" />
                                <span>Snapshot: {deploy.snapshot_name}</span>
                              </div>
                            )}
                            {deploy.snapshot_name && (
                              <div className="flex items-center gap-2">
                                {/* Botão Deletar (Standby / Visual Only) */}
                                <button
                                  disabled
                                  className="opacity-40 cursor-not-allowed bg-slate-100 text-slate-400 px-3 py-2 rounded-xl font-black text-[9px] uppercase tracking-widest transition-all flex items-center gap-1.5 justify-center"
                                  title="Cleanup and removal of physical template is managed by reaper background worker"
                                >
                                  <Trash2 size={11} />
                                  Delete
                                </button>

                                {/* Botão Abrir Checkpoint como Branch */}
                                <button
                                  onClick={() => handleCreateBranchFromSnapshot(deploy.snapshot_name)}
                                  className="bg-indigo-50 hover:bg-indigo-100 text-indigo-600 border border-indigo-100 px-3 py-2 rounded-xl font-black text-[9px] uppercase tracking-widest transition-all flex items-center gap-1.5 justify-center active:scale-95 shadow-sm hover:shadow"
                                >
                                  <FolderOpen size={11} />
                                  Open Checkpoint
                                </button>
                                
                                {/* Botão Restaurar (Real Rollback Action com Auto-Undo) */}
                                <button
                                  disabled={restoringId !== null || deploy.status === 'rolled_back'}
                                  onClick={() => handleRestoreDeploy(deploy.id)}
                                  className={`px-3 py-2 rounded-xl font-black text-[9px] uppercase tracking-widest transition-all flex items-center gap-1.5 justify-center active:scale-95 ${
                                    deploy.status === 'rolled_back'
                                      ? 'bg-slate-100 text-slate-500 cursor-default border border-slate-200'
                                      : 'bg-emerald-50 hover:bg-emerald-100 text-emerald-600 border border-emerald-100 shadow-sm hover:shadow'
                                  }`}
                                >
                                  {restoringId === deploy.id ? (
                                    <Loader2 size={11} className="animate-spin" />
                                  ) : (
                                    <RefreshCw size={11} />
                                  )}
                                  {deploy.status === 'rolled_back' ? 'Restored' : 'Restore State'}
                                </button>
                              </div>
                            )}
                          </div>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
