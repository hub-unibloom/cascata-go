import React, { useEffect, useRef, useState } from 'react';
import {
    Download,
    Eye,
    FileText,
    FileUp,
    Folder,
    FolderPlus,
    Key,
    Loader2,
    LockKeyhole,
    Plus,
    ShieldCheck,
    Terminal,
    Trash2,
    Vault,
    X,
    Zap,
    Shield,
    EyeOff,
    PenTool
} from 'lucide-react';

type VaultReleasePolicy = 'exportable' | 'runtime' | 'verify_only' | 'sign_only';
type VaultItemType = 'folder' | 'key' | 'cert' | 'env' | 'file' | 'basic_auth';

interface VaultPathItem {
    id: string;
    name: string;
}

interface VaultItem {
    id: string;
    name: string;
    type: VaultItemType | string;
    description?: string;
    metadata?: {
        release_policy?: VaultReleasePolicy;
        mime?: string;
        is_file?: boolean;
        [key: string]: any;
    };
}

interface NewSecretData {
    name: string;
    type: VaultItemType;
    value: string;
    description: string;
    mime: string;
    release_policy: VaultReleasePolicy;
}

interface ProjectVaultModalProps {
    projectId: string;
    open: boolean;
    onClose: () => void;
    onSuccess?: (message: string) => void;
    onError?: (message: string) => void;
}

const defaultSecretData: NewSecretData = {
    name: '',
    type: 'key',
    value: '',
    description: '',
    mime: 'text/plain',
    release_policy: 'runtime'
};

const itemTypes: Array<{ id: VaultItemType; label: string; icon: React.ElementType }> = [
    { id: 'folder', label: 'Folder', icon: Folder },
    { id: 'key', label: 'Key', icon: Key },
    { id: 'cert', label: 'Cert', icon: ShieldCheck },
    { id: 'env', label: 'Env', icon: Terminal },
    { id: 'file', label: 'File', icon: FileText },
    { id: 'basic_auth', label: 'Basic Auth', icon: LockKeyhole }
];

const releasePolicies: Array<{ id: VaultReleasePolicy; label: string; icon: React.ElementType }> = [
    { id: 'runtime', label: 'Runtime', icon: Zap },
    { id: 'exportable', label: 'Exportable', icon: Eye },
    { id: 'verify_only', label: 'Verify', icon: Shield },
    { id: 'sign_only', label: 'Sign', icon: PenTool }
];

const policyTone: Record<VaultReleasePolicy, string> = {
    runtime: 'bg-sky-500/10 text-sky-300 border-sky-500/20',
    exportable: 'bg-emerald-500/10 text-emerald-300 border-emerald-500/20',
    verify_only: 'bg-violet-500/10 text-violet-300 border-violet-500/20',
    sign_only: 'bg-rose-500/10 text-rose-300 border-rose-500/20'
};

const token = () => localStorage.getItem('cascata_token');

const copyToClipboard = async (text: string) => {
    if (!text) return;
    if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
        return;
    }

    const textArea = document.createElement('textarea');
    textArea.value = text;
    textArea.style.position = 'fixed';
    textArea.style.left = '-9999px';
    document.body.appendChild(textArea);
    textArea.focus();
    textArea.select();
    document.execCommand('copy');
    document.body.removeChild(textArea);
};

const getItemIcon = (type: string) => {
    const match = itemTypes.find(item => item.id === type);
    return match?.icon || Key;
};

const getPolicy = (item: VaultItem): VaultReleasePolicy => {
    const policy = item.metadata?.release_policy;
    if (policy === 'exportable' || policy === 'verify_only' || policy === 'sign_only' || policy === 'runtime') {
        return policy;
    }
    return item.type === 'folder' ? 'runtime' : 'runtime';
};

const ProjectVaultModal: React.FC<ProjectVaultModalProps> = ({ projectId, open, onClose, onSuccess, onError }) => {
    const [vaultPath, setVaultPath] = useState<VaultPathItem[]>([]);
    const [vaultItems, setVaultItems] = useState<VaultItem[]>([]);
    const [vaultLoading, setVaultLoading] = useState(false);
    const [showNewSecret, setShowNewSecret] = useState(false);
    const [newSecretData, setNewSecretData] = useState<NewSecretData>(defaultSecretData);
    
    // States para Basic Auth
    const [basicAuthId, setBasicAuthId] = useState('');
    const [basicAuthSecret, setBasicAuthSecret] = useState('');

    const fileInputRef = useRef<HTMLInputElement>(null);

    const notifyError = (message: string) => {
        if (onError) onError(message);
        else alert(message);
    };

    const fetchVaultItems = async (parentId: string | null = null) => {
        setVaultLoading(true);
        try {
            const res = await fetch(`/api/control/projects/${projectId}/vault?parentId=${parentId || 'root'}`, {
                headers: { Authorization: `Bearer ${token()}` }
            });
            const data = await res.json();
            if (!res.ok) throw new Error(data.error || 'Failed to load vault');
            setVaultItems(Array.isArray(data) ? data : []);
        } catch (e: any) {
            notifyError(e.message || 'Failed to load vault');
        } finally {
            setVaultLoading(false);
        }
    };

    useEffect(() => {
        if (!open) return;
        setVaultPath([]);
        fetchVaultItems(null);
    }, [open, projectId]);

    const resetNewSecret = () => {
        setNewSecretData(defaultSecretData);
        setBasicAuthId('');
        setBasicAuthSecret('');
        if (fileInputRef.current) fileInputRef.current.value = '';
    };

    const openNewSecret = (type: VaultItemType) => {
        setNewSecretData({
            ...defaultSecretData,
            type,
            release_policy: type === 'folder' ? 'runtime' : 'runtime'
        });
        setShowNewSecret(true);
    };

    const selectType = (type: VaultItemType) => {
        setNewSecretData(prev => ({
            ...prev,
            type,
            value: '',
            release_policy: type === 'folder' ? 'runtime' : prev.release_policy
        }));
    };

    const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;

        const reader = new FileReader();
        reader.onload = (event) => {
            const base64 = event.target?.result as string;
            const parts = base64.split(',');
            const mime = parts[0].match(/:(.*?);/)?.[1] || 'application/octet-stream';
            const rawData = parts[1] || '';

            setNewSecretData(prev => ({
                ...prev,
                name: prev.name || file.name,
                value: rawData,
                mime,
                type: 'file'
            }));
        };
        reader.readAsDataURL(file);
    };

    const handleCreateSecret = async () => {
        if (!newSecretData.name.trim()) return;
        if (newSecretData.type !== 'folder' && newSecretData.type !== 'basic_auth' && !newSecretData.value) return;
        if (newSecretData.type === 'basic_auth' && (!basicAuthId || !basicAuthSecret)) return;

        try {
            const currentFolder = vaultPath.length > 0 ? vaultPath[vaultPath.length - 1].id : 'root';
            const metadata = {
                mime: newSecretData.mime,
                is_file: newSecretData.type === 'file',
                release_policy: newSecretData.release_policy
            };

            let finalValue = newSecretData.value;
            if (newSecretData.type === 'basic_auth') {
                finalValue = JSON.stringify({ client_id: basicAuthId, client_secret: basicAuthSecret });
            }

            const res = await fetch(`/api/control/projects/${projectId}/vault`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    Authorization: `Bearer ${token()}`
                },
                body: JSON.stringify({
                    name: newSecretData.name.trim(),
                    type: newSecretData.type,
                    value: finalValue,
                    description: newSecretData.description,
                    parent_id: currentFolder,
                    release_policy: newSecretData.release_policy,
                    metadata
                })
            });

            const data = await res.json();
            if (!res.ok) throw new Error(data.error || 'Failed to store secret');

            onSuccess?.(newSecretData.type === 'folder' ? 'Folder created' : 'Secret stored safely');
            setShowNewSecret(false);
            resetNewSecret();
            fetchVaultItems(currentFolder === 'root' ? null : currentFolder);
        } catch (e: any) {
            notifyError(e.message || 'Failed to store secret');
        }
    };

    const handleRevealSecret = async (item: VaultItem) => {
        try {
            const res = await fetch(`/api/control/projects/${projectId}/vault/${item.id}/reveal`, {
                method: 'POST',
                headers: { Authorization: `Bearer ${token()}` }
            });
            const data = await res.json();
            if (!res.ok) throw new Error(data.error || 'Secret cannot be revealed');

            if (item.type === 'file' || data.meta?.is_file) {
                const mime = data.meta?.mime || 'application/octet-stream';
                const link = document.createElement('a');
                link.href = `data:${mime};base64,${data.value}`;
                link.download = item.name;
                document.body.appendChild(link);
                link.click();
                document.body.removeChild(link);
                onSuccess?.('File decrypted and downloading');
                return;
            }

            await copyToClipboard(data.value);
            onSuccess?.('Secret decrypted and copied');
        } catch (e: any) {
            notifyError(e.message || 'Failed to reveal secret');
        }
    };

    const handleDeleteSecret = async (id: string) => {
        if (!confirm('Delete this vault item permanently?')) return;
        try {
            const res = await fetch(`/api/control/projects/${projectId}/vault/${id}`, {
                method: 'DELETE',
                headers: { Authorization: `Bearer ${token()}` }
            });
            if (!res.ok) {
                const data = await res.json().catch(() => ({}));
                throw new Error(data.error || 'Failed to delete vault item');
            }
            const currentFolder = vaultPath.length > 0 ? vaultPath[vaultPath.length - 1].id : null;
            fetchVaultItems(currentFolder);
            onSuccess?.('Vault item deleted');
        } catch (e: any) {
            notifyError(e.message || 'Failed to delete vault item');
        }
    };

    if (!open) return null;

    return (
        <div className="fixed inset-0 bg-slate-950/90 backdrop-blur-md z-[700] flex items-center justify-center p-4 md:p-8 animate-in fade-in zoom-in-95">
            <div className="bg-slate-900 rounded-3xl w-full max-w-6xl h-[88vh] border border-slate-800 shadow-2xl flex flex-col overflow-hidden relative">
                <div className="px-6 py-5 md:px-8 md:py-6 border-b border-slate-800 flex flex-col gap-4 md:flex-row md:justify-between md:items-center bg-slate-900/70 backdrop-blur-xl shrink-0">
                    <div className="flex items-center gap-4 min-w-0">
                        <div className="w-12 h-12 bg-amber-500 rounded-2xl flex items-center justify-center shadow-[0_0_20px_rgba(245,158,11,0.35)] shrink-0">
                            <LockKeyhole size={24} className="text-slate-900" />
                        </div>
                        <div className="min-w-0">
                            <h3 className="text-2xl font-black text-white tracking-tight truncate">Project Vault</h3>
                            <p className="text-xs text-slate-400 font-medium uppercase tracking-widest mt-1">Encrypted Storage & Release Policies</p>
                        </div>
                    </div>
                    <div className="flex gap-3 justify-between md:justify-end">
                        <div className="flex bg-slate-800 p-1 rounded-xl">
                            <button onClick={() => openNewSecret('folder')} className="px-3 md:px-4 py-2 text-[10px] font-bold text-slate-300 hover:text-white uppercase hover:bg-slate-700 rounded-lg flex items-center gap-2 transition-all">
                                <FolderPlus size={14} /> Folder
                            </button>
                            <button onClick={() => openNewSecret('key')} className="px-3 md:px-4 py-2 text-[10px] font-bold text-amber-400 hover:text-amber-200 uppercase hover:bg-slate-700 rounded-lg flex items-center gap-2 transition-all">
                                <Plus size={14} /> Secret
                            </button>
                        </div>
                        <button onClick={onClose} className="p-3 bg-slate-800 hover:bg-slate-700 rounded-full text-slate-400 hover:text-white transition-colors" title="Close">
                            <X size={20} />
                        </button>
                    </div>
                </div>

                <div className="px-6 md:px-8 py-4 bg-slate-900/80 border-b border-slate-800 flex items-center gap-2 text-xs font-mono text-slate-400 overflow-x-auto">
                    <button onClick={() => { setVaultPath([]); fetchVaultItems(null); }} className="hover:text-amber-400 transition-colors shrink-0">ROOT</button>
                    {vaultPath.map((p, i) => (
                        <React.Fragment key={p.id}>
                            <span className="text-slate-600">/</span>
                            <button
                                onClick={() => {
                                    const newPath = vaultPath.slice(0, i + 1);
                                    setVaultPath(newPath);
                                    fetchVaultItems(p.id);
                                }}
                                className="hover:text-amber-400 transition-colors shrink-0"
                            >
                                {p.name}
                            </button>
                        </React.Fragment>
                    ))}
                </div>

                <div className="flex-1 overflow-y-auto p-6 md:p-8 bg-[#0B0F19]">
                    {vaultLoading ? (
                        <div className="flex justify-center items-center h-full">
                            <Loader2 size={32} className="animate-spin text-amber-500" />
                        </div>
                    ) : vaultItems.length === 0 ? (
                        <div className="flex flex-col items-center justify-center h-full text-slate-700 opacity-60">
                            <Vault size={64} className="mb-4" />
                            <p className="text-sm font-bold uppercase tracking-widest">Vault Empty</p>
                        </div>
                    ) : (
                        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
                            {vaultItems.map((item) => {
                                const ItemIcon = getItemIcon(item.type);
                                const policy = getPolicy(item);
                                const canReveal = policy === 'exportable';
                                return (
                                    <div
                                        key={item.id}
                                        onClick={() => {
                                            if (item.type === 'folder') {
                                                setVaultPath([...vaultPath, { id: item.id, name: item.name }]);
                                                fetchVaultItems(item.id);
                                            }
                                        }}
                                        className={`p-4 rounded-2xl border transition-all cursor-pointer group relative overflow-hidden min-h-[150px] ${item.type === 'folder'
                                            ? 'bg-slate-800/50 border-slate-700 hover:border-slate-600 hover:bg-slate-800'
                                            : 'bg-slate-900 border-slate-800 hover:border-amber-900/50 hover:bg-slate-800/30'}`}
                                    >
                                        <div className="flex justify-between items-start mb-4">
                                            <div className={`w-10 h-10 rounded-xl flex items-center justify-center ${item.type === 'folder' ? 'bg-slate-700 text-slate-300' : 'bg-amber-500/10 text-amber-400'}`}>
                                                <ItemIcon size={20} />
                                            </div>
                                            {item.type !== 'folder' && (
                                                <button
                                                    onClick={(e) => { e.stopPropagation(); handleRevealSecret(item); }}
                                                    disabled={!canReveal}
                                                    className={`p-2 rounded-lg transition-colors z-20 ${canReveal ? 'bg-slate-800 text-slate-400 hover:text-amber-400' : 'bg-slate-800/50 text-slate-600 cursor-not-allowed'}`}
                                                    title={canReveal ? (item.type === 'file' ? 'Download' : 'Reveal') : 'Policy blocks reveal'}
                                                >
                                                    {canReveal ? (item.type === 'file' ? <Download size={14} /> : <Eye size={14} />) : <EyeOff size={14} />}
                                                </button>
                                            )}
                                        </div>
                                        <h4 className="font-bold text-sm text-slate-200 truncate pr-6">{item.name}</h4>
                                        <div className="mt-3 flex flex-wrap gap-2">
                                            <span className="text-[9px] text-slate-400 bg-slate-800 border border-slate-700 px-2 py-1 rounded-lg uppercase tracking-widest">{item.type}</span>
                                            {item.type !== 'folder' && (
                                                <span className={`text-[9px] border px-2 py-1 rounded-lg uppercase tracking-widest ${policyTone[policy]}`}>{policy.replace('_', ' ')}</span>
                                            )}
                                        </div>

                                        <button
                                            onClick={(e) => { e.stopPropagation(); handleDeleteSecret(item.id); }}
                                            className="absolute bottom-4 right-4 text-slate-600 hover:text-rose-500 opacity-0 group-hover:opacity-100 transition-opacity"
                                            title="Delete"
                                        >
                                            <Trash2 size={14} />
                                        </button>
                                    </div>
                                );
                            })}
                        </div>
                    )}
                </div>

                {showNewSecret && (
                    <div className="absolute inset-0 bg-slate-950/80 backdrop-blur-sm flex items-center justify-center p-4 md:p-8 z-50 animate-in fade-in">
                        <div className="bg-slate-900 border border-slate-700 rounded-3xl p-6 md:p-8 w-full max-w-2xl shadow-2xl max-h-full overflow-y-auto">
                            <h4 className="text-xl font-black text-white mb-6">New {newSecretData.type === 'folder' ? 'Folder' : 'Secret'}</h4>
                            <div className="space-y-5">
                                <div>
                                    <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest ml-1">Type</label>
                                    <div className="grid grid-cols-5 gap-2 mt-2">
                                        {itemTypes.map(({ id, label, icon: Icon }) => (
                                            <button
                                                key={id}
                                                onClick={() => selectType(id)}
                                                className={`h-12 rounded-xl text-[10px] font-bold uppercase transition-all flex items-center justify-center gap-2 ${newSecretData.type === id ? 'bg-amber-500 text-slate-900' : 'bg-slate-800 text-slate-400 hover:bg-slate-700'}`}
                                            >
                                                <Icon size={14} /> <span className="hidden sm:inline">{label}</span>
                                            </button>
                                        ))}
                                    </div>
                                </div>

                                {newSecretData.type !== 'folder' && (
                                    <div>
                                        <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest ml-1">Release Policy</label>
                                        <div className="grid grid-cols-2 md:grid-cols-4 gap-2 mt-2">
                                            {releasePolicies.map(({ id, label, icon: Icon }) => (
                                                <button
                                                    key={id}
                                                    onClick={() => setNewSecretData(prev => ({ ...prev, release_policy: id }))}
                                                    className={`h-12 rounded-xl text-[10px] font-bold uppercase transition-all flex items-center justify-center gap-2 border ${newSecretData.release_policy === id ? policyTone[id] : 'bg-slate-800 text-slate-400 border-slate-700 hover:bg-slate-700'}`}
                                                >
                                                    <Icon size={14} /> {label}
                                                </button>
                                            ))}
                                        </div>
                                        <p className="text-[10px] text-slate-400 mt-2 ml-1 leading-relaxed">
                                            {newSecretData.release_policy === 'runtime' && "⚡ Runtime: Accessible internally by Edge Functions (via env.NAME) and Automations. Safe from UI leakage."}
                                            {newSecretData.release_policy === 'exportable' && "👁️ Exportable: Can be decrypted and viewed directly in the UI. Also available to runtime code."}
                                            {newSecretData.release_policy === 'verify_only' && "🛡️ Verify: Used for signature verification (e.g. webhooks). Never exposed to code or UI."}
                                            {newSecretData.release_policy === 'sign_only' && "🖋️ Sign: Used exclusively for data signing. Never exposed to code or UI."}
                                        </p>
                                    </div>
                                )}

                                <div>
                                    <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest ml-1">Name</label>
                                    <input
                                        autoFocus
                                        value={newSecretData.name}
                                        onChange={e => setNewSecretData({ ...newSecretData, name: e.target.value })}
                                        className="w-full bg-slate-800 border border-slate-700 rounded-xl px-4 py-3 text-sm font-bold text-white outline-none focus:border-amber-500"
                                    />
                                </div>

                                {newSecretData.type !== 'folder' && (
                                    <div>
                                        <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest ml-1">Content</label>
                                        {newSecretData.type === 'file' || newSecretData.type === 'cert' ? (
                                            <div className="w-full bg-slate-800 border-2 border-dashed border-slate-700 rounded-xl p-6 text-center cursor-pointer hover:border-amber-500 transition-colors relative">
                                                <input
                                                    type="file"
                                                    ref={fileInputRef}
                                                    onChange={handleFileUpload}
                                                    className="absolute inset-0 opacity-0 cursor-pointer"
                                                />
                                                <FileUp className="mx-auto text-slate-400 mb-2" />
                                                <p className="text-xs text-slate-300 font-bold">{newSecretData.value ? 'File Selected' : 'Select File'}</p>
                                            </div>
                                        ) : newSecretData.type === 'basic_auth' ? (
                                            <div className="space-y-3">
                                                <div>
                                                    <label className="text-[9px] font-black text-slate-500 uppercase tracking-widest ml-1 block mb-1">Client ID</label>
                                                    <input
                                                        value={basicAuthId}
                                                        onChange={e => setBasicAuthId(e.target.value)}
                                                        className="w-full bg-slate-800 border border-slate-700 rounded-xl px-4 py-3 text-xs font-mono text-emerald-400 outline-none focus:border-amber-500"
                                                        placeholder="Ex: whatsapp_client_id_123"
                                                    />
                                                </div>
                                                <div>
                                                    <label className="text-[9px] font-black text-slate-500 uppercase tracking-widest ml-1 block mb-1">Client Secret</label>
                                                    <input
                                                        type="password"
                                                        value={basicAuthSecret}
                                                        onChange={e => setBasicAuthSecret(e.target.value)}
                                                        className="w-full bg-slate-800 border border-slate-700 rounded-xl px-4 py-3 text-xs font-mono text-emerald-400 outline-none focus:border-amber-500"
                                                        placeholder="Ex: secret_xyz789..."
                                                    />
                                                </div>
                                            </div>
                                        ) : (
                                            <textarea
                                                value={newSecretData.value}
                                                onChange={e => setNewSecretData({ ...newSecretData, value: e.target.value })}
                                                className="w-full bg-slate-800 border border-slate-700 rounded-xl px-4 py-3 text-xs font-mono text-emerald-400 outline-none focus:border-amber-500 min-h-[110px]"
                                            />
                                        )}
                                    </div>
                                )}

                                <div>
                                    <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest ml-1">Description</label>
                                    <input
                                        value={newSecretData.description}
                                        onChange={e => setNewSecretData({ ...newSecretData, description: e.target.value })}
                                        className="w-full bg-slate-800 border border-slate-700 rounded-xl px-4 py-3 text-xs text-slate-400 outline-none focus:border-amber-500"
                                    />
                                </div>
                                <div className="flex gap-4 pt-2">
                                    <button onClick={() => { setShowNewSecret(false); resetNewSecret(); }} className="flex-1 py-3 text-xs font-bold text-slate-500 hover:text-white">Cancel</button>
                                    <button onClick={handleCreateSecret} className="flex-[2] bg-amber-500 text-slate-900 py-3 rounded-xl font-black text-xs uppercase tracking-widest hover:bg-amber-400 shadow-lg">Save to Vault</button>
                                </div>
                            </div>
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
};

export default ProjectVaultModal;
