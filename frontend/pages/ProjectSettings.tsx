
import React, { useState, useEffect } from 'react';
import {
    Shield, Key, Globe, Lock, Save, Loader2, CheckCircle2, Copy,
    Terminal, Eye, EyeOff, RefreshCw, Code, BookOpen, AlertTriangle,
    Server, ExternalLink, Plus, X, Link, CloudLightning, FileText, Info, Trash2,
    Archive, Download, Upload, HardDrive, FileJson, Database, Zap, Network, Scale, Layers,
    Smartphone, MessageSquare, Clock, RotateCcw, Calendar, Play, Vault,
    Folder, FolderPlus, FileKey, FileCode, ChevronRight, LockKeyhole, ShieldCheck, FileUp,
    ScanEye, Settings2
} from 'lucide-react';
import ProjectVaultModal from '../components/ProjectVaultModal';

// Helper: Converte Punycode (xn--...) para Unicode (legível)
const punycodeToUnicode = (domain: string): string => {
    if (!domain || !domain.includes('xn--')) return domain;
    try {
        // Usar API de decode de Punycode
        return domain.replace(/xn--[a-z0-9-]+/gi, (match) => {
            try {
                // Decodificar Punycode manualmente
                const base = match.replace('xn--', '');
                let result = '';
                let i = base.length - 1;
                let bias = 72;
                let n = 0x80;
                
                while (i >= 0) {
                    if (base[i] === '-') {
                        i--;
                        continue;
                    }
                    // Simplificação: usar decodeURIComponent para caracteres básicos
                    return match; // Fallback: retorna original se complexo
                }
                return match;
            } catch {
                return match;
            }
        });
    } catch {
        return domain;
    }
};

// Helper: Converte Unicode para Punycode (para enviar ao backend)
const unicodeToPunycode = (domain: string): string => {
    if (!domain || /^[a-zA-Z0-9.-]+$/.test(domain)) return domain.toLowerCase().trim();
    // Para domínios simples ASCII, retorna como está
    // Domínios complexos com caracteres especiais precisariam de biblioteca punycode
    return domain.toLowerCase().trim();
};

// Helper: Detecta se domínio tem caracteres IDN (não-ASCII)
const hasIDNChars = (domain: string): boolean => {
    return /[^\x00-\x7F]/.test(domain);
};

const ProjectSettings: React.FC<{ projectId: string }> = ({ projectId }) => {
    const [project, setProject] = useState<any>(null);
    const [customDomain, setCustomDomain] = useState('');
    const [displayDomain, setDisplayDomain] = useState(''); // Unicode para exibição
    const [availableCerts, setAvailableCerts] = useState<string[]>([]);
    const [loading, setLoading] = useState(true);
    const [rotating, setRotating] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);
    const [success, setSuccess] = useState<string | null>(null);
    const [error, setError] = useState<string | null>(null);

    // Database Config State
    const [dbConfig, setDbConfig] = useState<{ maxConnections: number, idleTimeout: number, statementTimeout: number }>({
        maxConnections: 10,
        idleTimeout: 60,
        statementTimeout: 15000 // Default 15s
    });

    // Timezone
    const [timezone, setTimezone] = useState('UTC');

    // BYOD / Ejection State
    const [isEjected, setIsEjected] = useState(false);
    const [externalDbUrl, setExternalDbUrl] = useState('');
    const [readReplicaUrl, setReadReplicaUrl] = useState('');

    // Firebase State
    const [firebaseJson, setFirebaseJson] = useState('');
    const [hasFirebase, setHasFirebase] = useState(false);

    // Security State
    const [revealedKeyValues, setRevealedKeyValues] = useState<Record<string, string>>({});

    // Origins State
    const [origins, setOrigins] = useState<any[]>([]);
    const [newOrigin, setNewOrigin] = useState('');

    // --- MCP Security Perimeter ---
    const [mcpIps, setMcpIps] = useState<string[]>([]);
    const [newMcpIp, setNewMcpIp] = useState('');
    const [mcpUrls, setMcpUrls] = useState<string[]>([]);
    const [newMcpUrl, setNewMcpUrl] = useState('');
    const [mcpMaxRows, setMcpMaxRows] = useState(100);
    const [mcpEnabled, setMcpEnabled] = useState(false);

    // Verification Modal State
    const [showVerifyModal, setShowVerifyModal] = useState(false);
    const [verifyPassword, setVerifyPassword] = useState('');
    const [showDomainSuggestions, setShowDomainSuggestions] = useState(false);

    // Vault State
    const [showVault, setShowVault] = useState(false);
    const [isVaultUnlocked, setIsVaultUnlocked] = useState(false);

    type SecurityIntent =
        | { type: 'REVEAL_KEY', keyType: string }
        | { type: 'ROTATE_KEY', keyType: string }
        | { type: 'DELETE_DOMAIN' }
        | { type: 'UNLOCK_VAULT' };

    const [pendingIntent, setPendingIntent] = useState<SecurityIntent | null>(null);
    const [verifyLoading, setVerifyLoading] = useState(false);

    // MFA Downgrade / Upgrade States
    const [mfaConfirm, setMfaConfirm] = useState<{
        otpRequired: boolean;
        onSubmit: (password: string, otp: string) => Promise<void>;
        onCancel: () => void;
    } | null>(null);
    const [mfaPassword, setMfaPassword] = useState('');
    const [mfaOtp, setMfaOtp] = useState('');

    // Backup State
    const [exporting, setExporting] = useState(false);

    // --- UI LOGIC ---
    const isInputDirty = customDomain !== (project?.custom_domain || '');

    const bestCertMatch = availableCerts.find(cert => {
        if (cert === customDomain) return true;
        if (cert.startsWith('*.')) {
            const root = cert.slice(2);
            if (customDomain.endsWith(root)) {
                const domainParts = customDomain.split('.');
                const rootParts = root.split('.');
                return domainParts.length === rootParts.length + 1;
            }
        }
        return false;
    });

    // Inteligência de Autocomplete para Domínios
    const getDomainSuggestions = () => {
        const search = displayDomain.toLowerCase().trim();
        const suggestions: any[] = [];
        const seen = new Set();

        availableCerts.forEach(cert => {
            const unicodeCert = punycodeToUnicode(cert);
            
            // 1. Se não há busca, mostrar certs originais
            if (!search) {
                if (!seen.has(unicodeCert)) {
                    suggestions.push({ value: cert, label: unicodeCert, type: cert.startsWith('*.') ? 'wildcard' : 'exact' });
                    seen.add(unicodeCert);
                }
                return;
            }

            // 2. Match direto no nome do certificado
            if (unicodeCert.toLowerCase().includes(search)) {
                if (!seen.has(unicodeCert)) {
                    suggestions.push({ value: cert, label: unicodeCert, type: cert.startsWith('*.') ? 'wildcard' : 'exact' });
                    seen.add(unicodeCert);
                }
            }

            // 3. Inteligência Wildcard Expansion
            // Se o cert é *.unibloom.com.br e o usuário digitou "dash", sugerir "dash.unibloom.com.br"
            if (unicodeCert.startsWith('*.')) {
                const root = unicodeCert.slice(2);
                // Se o usuário digitou algo que não contém pontos, ou se o que ele digitou é o início de um subdomínio
                if (!search.includes('.') && search.length > 0) {
                    const expanded = `${search}.${root}`;
                    if (!seen.has(expanded)) {
                        suggestions.push({ 
                            value: unicodeToPunycode(expanded), 
                            label: expanded, 
                            type: 'expansion',
                            source: unicodeCert 
                        });
                        seen.add(expanded);
                    }
                }
            }
        });

        return suggestions;
    };

    const domainSuggestions = getDomainSuggestions();

    const copyToClipboard = (text: string) => {
        if (!text) return;
        if (navigator.clipboard && window.isSecureContext) {
            navigator.clipboard.writeText(text)
                .then(() => { setSuccess("Copiado!"); setTimeout(() => setSuccess(null), 2000); })
                .catch(() => alert("Erro ao copiar (HTTPS)."));
            return;
        }
        try {
            const textArea = document.createElement("textarea");
            textArea.value = text;
            textArea.style.position = "fixed";
            textArea.style.left = "-9999px";
            document.body.appendChild(textArea);
            textArea.focus();
            textArea.select();
            document.execCommand('copy');
            document.body.removeChild(textArea);
            setSuccess("Copiado!");
            setTimeout(() => setSuccess(null), 2000);
        } catch (err) { alert("Erro ao copiar."); }
    };

    const fetchProject = async () => {
        try {
            const res = await fetch('/api/control/projects', {
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            const data = await res.json();
            console.log('[DEBUG] Projects data from API:', data);
            const current = data.find((p: any) => p.slug === projectId);
            console.log('[DEBUG] Current project:', current);
            console.log('[DEBUG] custom_domain value:', current?.custom_domain);

            if (current) {
                setProject(current);
                // Converter Punycode para Unicode para exibição
                const rawDomain = current.custom_domain || '';
                console.log('[DEBUG] rawDomain:', rawDomain);
                const unicodeDomain = punycodeToUnicode(rawDomain);
                console.log('[DEBUG] unicodeDomain:', unicodeDomain);
                setCustomDomain(rawDomain); // Punycode para API
                setDisplayDomain(unicodeDomain); // Unicode para exibição
                setMcpEnabled(current.metadata?.ai_governance?.mcp_enabled ?? true);

                // --- MCP Governance ---
                const mcpGov = current.metadata?.ai_governance || {};
                const mcpPerimeter = mcpGov.mcp_perimeter || {};
                setMcpIps(mcpPerimeter.allowed_ips || mcpGov.allowed_ips || []);
                setMcpUrls(mcpPerimeter.allowed_urls || mcpGov.allowed_urls || []);
                setMcpMaxRows(mcpGov.max_rows || 100);

                const rawOrigins = current.metadata?.allowed_origins || [];
                setOrigins(rawOrigins.map((o: any) => typeof o === 'string' ? { url: o, require_auth: true } : o));

                // Load Timezone
                setTimezone(current.metadata?.timezone || 'UTC');

                if (current.metadata?.db_config) {
                    setDbConfig({
                        max_connections: current.metadata.db_config.max_connections || current.metadata.db_config.maxConnections || 10,
                        maxConnections: current.metadata.db_config.max_connections || current.metadata.db_config.maxConnections || 10,
                        idleTimeout: current.metadata.db_config.idleTimeout || 60,
                        statementTimeout: current.metadata.db_config.statementTimeout || 15000
                    });
                }

                // Load BYOD State
                if (current.metadata?.external_db_url) {
                    setIsEjected(true);
                    setExternalDbUrl(current.metadata.external_db_url);
                    setReadReplicaUrl(current.metadata.read_replica_url || '');
                } else {
                    setIsEjected(false);
                    setExternalDbUrl('');
                    setReadReplicaUrl('');
                }

                if (current.metadata?.firebase_config) {
                    setHasFirebase(true);
                }
            }

            fetchAvailableCerts();
        } catch (e) {
            console.error("Failed to sync project settings");
        } finally {
            setLoading(false);
        }
    };

    const fetchAvailableCerts = async () => {
        try {
            const certRes = await fetch('/api/control/system/certificates/status', {
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            const certData = await certRes.json();
            setAvailableCerts(certData.domains || []);
        } catch (e) { console.error("Cert list failed"); }
    };

    useEffect(() => { fetchProject(); }, [projectId]);

    // Fechar dropdown de sugestões quando clicar fora
    useEffect(() => {
        const handleClickOutside = (e: MouseEvent) => {
            const target = e.target as HTMLElement;
            if (!target.closest('.domain-suggestions-container')) {
                setShowDomainSuggestions(false);
            }
        };
        document.addEventListener('mousedown', handleClickOutside);
        return () => document.removeEventListener('mousedown', handleClickOutside);
    }, []);

    // --- ACTIONS ---

    const handleVerifyAndExecute = async (e?: React.FormEvent) => {
        if (e) e.preventDefault();
        if (!verifyPassword) { alert("Digite a senha."); return; }
        if (!pendingIntent) return;

        setVerifyLoading(true);

        // Handling Standard Security Actions
        if (pendingIntent.type !== 'UNLOCK_VAULT') {
            try {
                if (pendingIntent.type === 'REVEAL_KEY') {
                    const keyType = pendingIntent.keyType === 'service' ? 'service_key' : pendingIntent.keyType === 'anon' ? 'anon_key' : 'jwt_secret';
                    const res = await fetch(`/api/control/projects/${projectId}/reveal-key`, {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                        body: JSON.stringify({ password: verifyPassword, keyType: keyType })
                    });
                    const data = await res.json();
                    if (!res.ok) { alert(data.error || "Senha incorreta."); } else {
                        setRevealedKeyValues(prev => ({ ...prev, [pendingIntent.keyType]: data.key }));
                        setTimeout(() => { setRevealedKeyValues(prev => { const updated = { ...prev }; delete updated[pendingIntent.keyType]; return updated; }); }, 60000);
                        setShowVerifyModal(false); setVerifyPassword('');
                    }
                } else {
                    const verifyRes = await fetch('/api/control/auth/verify', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                        body: JSON.stringify({ password: verifyPassword })
                    });

                    if (!verifyRes.ok) { alert("Senha incorreta."); setVerifyLoading(false); return; }

                    setShowVerifyModal(false);
                    setVerifyPassword('');

                    if (pendingIntent.type === 'ROTATE_KEY') await executeRotateKey(pendingIntent.keyType);
                    else if (pendingIntent.type === 'DELETE_DOMAIN') await executeDeleteDomain();
                }
            } catch (e) { alert("Erro de conexão."); }
            finally { setVerifyLoading(false); setPendingIntent(null); }
            return;
        }

        // Vault Unlock Logic
        try {
            const verifyRes = await fetch('/api/control/auth/verify', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({ password: verifyPassword })
            });

            if (!verifyRes.ok) {
                alert("Acesso Negado: Senha Incorreta.");
            } else {
                setIsVaultUnlocked(true);
                setShowVault(true);
                setShowVerifyModal(false);
                setVerifyPassword('');
            }
        } catch (e) { alert("Erro ao verificar senha."); }
        finally { setVerifyLoading(false); setPendingIntent(null); }
    };

    const executeRotateKey = async (type: string) => {
        setRotating(type);
        try {
            await fetch(`/api/control/projects/${projectId}/rotate-keys`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }, body: JSON.stringify({ type }) });
            await fetchProject();
            setSuccess(`${type.toUpperCase()} rotacionada.`);
            const next = { ...revealedKeyValues }; delete next[type.replace('_key', '').replace('_secret', '')]; setRevealedKeyValues(next);
            setTimeout(() => setSuccess(null), 3000);
        } catch (e) { alert('Falha ao rotacionar chave.'); } finally { setRotating(null); }
    };

    const executeDeleteDomain = async (password?: string, otp?: string) => {
        setSaving(true);
        try {
            const payload: any = { custom_domain: null };
            if (password) payload.password = password;
            if (otp) payload.otp_code = otp;

            const res = await fetch(`/api/control/projects/${projectId}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify(payload)
            });

            if (!res.ok) {
                const data = await res.json().catch(() => ({}));
                if (res.status === 403 && data.error === 'MFA_REQUIRED') {
                    setMfaConfirm({
                        otpRequired: !!data.otp_required,
                        onSubmit: async (pwd, token) => {
                            await executeDeleteDomain(pwd, token);
                        },
                        onCancel: () => {
                            setMfaConfirm(null);
                            setMfaPassword('');
                            setMfaOtp('');
                        }
                    });
                    return;
                }
                throw new Error(data.error || `HTTP ${res.status}`);
            }

            setSuccess('Domínio desvinculado.');
            setMfaConfirm(null);
            setMfaPassword('');
            setMfaOtp('');
            setProject((prev: any) => ({ ...prev, custom_domain: null }));
            setCustomDomain('');
            setTimeout(() => { fetchProject(); setSuccess(null); }, 1500);
        } catch (e: any) {
            alert(e.message || 'Erro ao remover domínio.');
        } finally {
            setSaving(false);
        }
    };

    const handleUpdateSettings = async (overrideOrigins?: any[], password?: string, otp?: string) => {
        setSaving(true);
        try {
            // Validate External DB if ejected
            if (isEjected) {
                if (!externalDbUrl.startsWith('postgres://') && !externalDbUrl.startsWith('postgresql://')) {
                    throw new Error("External DB URL must start with postgres:// or postgresql://");
                }
                if (readReplicaUrl && !readReplicaUrl.startsWith('postgres')) {
                    throw new Error("Read Replica URL invalid format");
                }
            }

            const payload: any = { custom_domain: customDomain };
            const metaUpdate: any = {
                db_config: dbConfig,
                // Timezone is read-only here, not sent back
                external_db_url: isEjected ? externalDbUrl : null,
                read_replica_url: isEjected && readReplicaUrl ? readReplicaUrl : null,
                ai_governance: {
                    ...(project?.metadata?.ai_governance || {}),
                    mcp_enabled: mcpEnabled,
                    mcp_perimeter: {
                        allowed_ips: mcpIps,
                        allowed_urls: mcpUrls
                    },
                    max_rows: mcpMaxRows
                }
            };

            if (overrideOrigins) {
                // Extract just the URLs (strings) from the origin objects for backend compatibility
                metaUpdate.allowed_origins = overrideOrigins.map((o: any) => typeof o === 'string' ? o : o.url);
            }

            payload.metadata = metaUpdate;
            if (password) payload.password = password;
            if (otp) payload.otp_code = otp;

            const res = await fetch(`/api/control/projects/${projectId}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify(payload)
            });

            if (!res.ok) {
                const data = await res.json().catch(() => ({}));
                if (res.status === 403 && data.error === 'MFA_REQUIRED') {
                    setMfaConfirm({
                        otpRequired: !!data.otp_required,
                        onSubmit: async (pwd, token) => {
                            await handleUpdateSettings(overrideOrigins, pwd, token);
                        },
                        onCancel: () => {
                            setMfaConfirm(null);
                            setMfaPassword('');
                            setMfaOtp('');
                        }
                    });
                    return;
                }
                throw new Error(data.error || `HTTP ${res.status}`);
            }

            setSuccess('Configuração salva. Migração de dados (se necessária) concluída.');
            setMfaConfirm(null);
            setMfaPassword('');
            setMfaOtp('');
            if (!overrideOrigins) fetchProject();
            setTimeout(() => setSuccess(null), 3000);
        } catch (e: any) {
            alert(e.message || 'Erro ao salvar/migrar.');
        } finally {
            setSaving(false);
        }
    };

    const handleSaveFirebase = async () => {
        setSaving(true);
        try {
            let firebaseConfig;
            try {
                firebaseConfig = JSON.parse(firebaseJson);
            } catch (e) {
                throw new Error("JSON Inválido.");
            }

            if (!firebaseConfig.project_id || !firebaseConfig.private_key || !firebaseConfig.client_email) {
                throw new Error("JSON incompleto. Requer project_id, private_key, e client_email.");
            }

            const newMeta = { ...project.metadata, firebase_config: firebaseConfig };

            await fetch(`/api/control/projects/${projectId}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({ metadata: newMeta })
            });

            setSuccess("FCM Configurado com sucesso!");
            setHasFirebase(true);
            setFirebaseJson(''); // Clear input for security
            fetchProject();
            setTimeout(() => setSuccess(null), 2000);
        } catch (e: any) {
            alert(e.message);
        } finally {
            setSaving(false);
        }
    };

    const toggleSchemaExposure = async () => {
        if (!project) return;
        setSaving(true);
        try {
            const current = project.metadata?.schema_exposure || false;
            const newMetadata = { ...project.metadata, schema_exposure: !current };

            const res = await fetch(`/api/control/projects/${projectId}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({ metadata: newMetadata })
            });

            if (res.ok) {
                setProject({ ...project, metadata: newMetadata });
                setSuccess(!current ? "Discovery Enabled (Public Swagger)" : "Discovery Disabled (Secure Mode)");
                setTimeout(() => setSuccess(null), 2000);
            }
        } catch (e) {
            alert("Falha ao atualizar.");
        } finally {
            setSaving(false);
        }
    };

    const addOrigin = () => {
        if (!newOrigin) return;
        try { new URL(newOrigin); } catch { alert('URL inválida.'); return; }
        const updated = [...origins, { url: newOrigin, require_auth: true }];
        setOrigins(updated); setNewOrigin(''); handleUpdateSettings(updated);
    };

    const removeOrigin = (url: string) => {
        const updated = origins.filter(o => o.url !== url);
        setOrigins(updated); handleUpdateSettings(updated);
    };

    const handleDownloadBackup = async () => {
        setExporting(true);
        try {
            const res = await fetch(`/api/control/projects/${projectId}/export`, { headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` } });
            if (!res.ok) throw new Error("Download failed");
            const blob = await res.blob();
            const url = window.URL.createObjectURL(blob);
            const a = document.createElement('a'); a.href = url; a.download = `${project.slug}_backup.caf`; document.body.appendChild(a); a.click(); window.URL.revokeObjectURL(url); document.body.removeChild(a);
        } catch (e) { alert("Erro ao baixar backup."); } finally { setExporting(false); }
    };

    // --- UI HANDLERS ---
    const handleRevealClick = (keyType: string) => {
        if (revealedKeyValues[keyType]) {
            const next = { ...revealedKeyValues }; delete next[keyType]; setRevealedKeyValues(next); return;
        }
        setPendingIntent({ type: 'REVEAL_KEY', keyType }); setShowVerifyModal(true);
    };
    const handleRotateClick = (keyType: string) => { setPendingIntent({ type: 'ROTATE_KEY', keyType }); setShowVerifyModal(true); };

    const handleSaveDomainClick = () => {
        if (!customDomain) { alert("Digite um domínio."); return; }
        handleUpdateSettings();
    };

    const handleDeleteDomainClick = () => {
        executeDeleteDomain();
    };

    const handleOpenVault = () => {
        if (isVaultUnlocked) {
            setShowVault(true);
        } else {
            setPendingIntent({ type: 'UNLOCK_VAULT' });
            setShowVerifyModal(true);
        }
    };

    if (loading) return <div className="p-20 flex justify-center"><Loader2 className="animate-spin text-indigo-600" /></div>;

    const apiEndpoint = project?.custom_domain ? `https://${project.custom_domain}` : `${window.location.origin}/api/data/${project?.slug}`;
    const sdkCode = `import { createClient } from './lib/cascata-sdk';\nconst cascata = createClient('${apiEndpoint}', '${project?.anon_key || 'anon_key'}');`;

    return (
        <div className="animate-in fade-in slide-in-from-bottom-4 duration-500 space-y-8 pb-40">
            {success && <div className="fixed top-8 left-1/2 -translate-x-1/2 z-[500] p-5 rounded-3xl bg-indigo-600 text-white shadow-2xl flex items-center gap-4 animate-bounce"><CheckCircle2 size={20} /><span className="text-sm font-black uppercase tracking-tight">{success}</span></div>}

            {/* BENTO GRID LAYOUT */}
            <div className="grid grid-cols-1 xl:grid-cols-3 gap-8">

                {/* COL 1: CONNECTION & KEYS & VAULT */}
                <div className="xl:col-span-1 space-y-8">
                    {/* Keys Card */}
                    <div className="bg-white border border-slate-200 rounded-[3rem] p-8 shadow-sm h-fit">
                        <h3 className="text-xl font-black text-slate-900 tracking-tight flex items-center gap-3 mb-6"><Key size={20} className="text-indigo-600" /> API Keys</h3>
                        <div className="space-y-6">
                            <KeyControl label="Anon Key" value={project?.anon_key || '******'} isSecret={false} isRevealed={true} onCopy={() => copyToClipboard(project?.anon_key)} onRotate={() => handleRotateClick('anon')} />
                            <KeyControl label="Service Key (Root)" value={revealedKeyValues['service'] || '••••••••••••••••'} isSecret={true} isRevealed={!!revealedKeyValues['service']} onReveal={() => handleRevealClick('service')} onRotate={() => handleRotateClick('service')} onCopy={() => copyToClipboard(revealedKeyValues['service'])} />
                            <KeyControl label="JWT Secret" value={revealedKeyValues['jwt_secret'] || '••••••••••••••••'} isSecret={true} isRevealed={!!revealedKeyValues['jwt_secret']} onReveal={() => handleRevealClick('jwt_secret')} onRotate={() => handleRotateClick('jwt_secret')} onCopy={() => copyToClipboard(revealedKeyValues['jwt_secret'])} />
                        </div>
                    </div>

                    {/* SECURE VAULT CARD (NEW) */}
                    <button
                        onClick={handleOpenVault}
                        className="w-full bg-slate-950 border border-slate-800 rounded-[3rem] p-8 shadow-2xl relative overflow-hidden group text-left"
                    >
                        <div className="absolute top-0 right-0 p-8 opacity-10 group-hover:scale-125 transition-transform"><Vault size={80} className="text-amber-500" /></div>
                        <div className="relative z-10">
                            <h3 className="text-xl font-black text-white tracking-tight flex items-center gap-3 mb-2"><LockKeyhole size={20} className="text-amber-500" /> Secure Vault</h3>
                            <p className="text-slate-400 text-xs font-medium">Encrypted storage for certificates, tokens, and secrets.</p>
                            <div className="mt-6 flex items-center gap-2 text-[10px] font-black uppercase tracking-widest text-amber-500 bg-amber-500/10 px-3 py-1.5 rounded-xl w-fit">
                                {isVaultUnlocked ? 'UNLOCKED' : 'LOCKED • ADMIN ACCESS ONLY'}
                            </div>
                        </div>
                    </button>

                    {/* SDK Helper */}
                    <div className="bg-slate-900 border border-slate-800 rounded-[3rem] p-8 shadow-lg relative overflow-hidden group">
                        <div className="absolute top-0 right-0 p-8 opacity-10 group-hover:scale-125 transition-transform"><Terminal size={100} className="text-white" /></div>
                        <h3 className="text-xl font-black text-white tracking-tight flex items-center gap-3 mb-4 relative z-10"><Code size={20} className="text-emerald-400" /> Quick Connect</h3>
                        <div className="relative z-10 group/code">
                            <pre className="bg-black/30 p-4 rounded-2xl text-[10px] text-emerald-400 font-mono overflow-x-auto whitespace-pre-wrap">{sdkCode}</pre>
                            <button onClick={() => copyToClipboard(sdkCode)} className="absolute top-2 right-2 p-1.5 bg-white/10 hover:bg-white/20 rounded-lg text-white opacity-0 group-hover/code:opacity-100 transition-opacity"><Copy size={14} /></button>
                        </div>
                    </div>

                    {/* DATA SOVEREIGNTY CARD - Movido para coluna 1 */}
                    <div className="bg-slate-900 border border-slate-800 rounded-[3rem] p-8 shadow-lg relative overflow-hidden group">
                        <div className="absolute top-0 right-0 p-8 opacity-5 group-hover:scale-110 transition-transform duration-700"><Archive size={100} className="text-white" /></div>
                        <div className="relative z-10">
                            <h3 className="text-xl font-black text-white tracking-tight flex items-center gap-3 mb-2"><div className="w-10 h-10 bg-indigo-600 text-white rounded-xl flex items-center justify-center shadow-lg"><HardDrive size={20} /></div>Data Sovereignty</h3>
                            <p className="text-slate-400 font-medium text-xs leading-relaxed mb-4">Generate a cryptographic snapshot (.CAF) with your Database, Vectors, and Storage assets.</p>
                            <button onClick={handleDownloadBackup} disabled={exporting} className="w-full bg-white text-slate-900 px-4 py-3 rounded-2xl font-black text-[10px] uppercase tracking-widest hover:bg-indigo-50 transition-all flex items-center justify-center gap-2 shadow-lg active:scale-95 disabled:opacity-70">
                                {exporting ? <Loader2 size={14} className="animate-spin text-indigo-600" /> : <Download size={14} className="text-indigo-600" />}Download Snapshot
                            </button>
                        </div>
                    </div>
                </div>

                {/* COL 2: INFRASTRUCTURE & DOMAIN */}
                <div className="xl:col-span-2 space-y-8">

                    {/* Global Limits & Localization */}
                    <div className="bg-white border border-slate-200 rounded-[4rem] p-12 shadow-sm relative overflow-hidden">
                        <h3 className="text-2xl font-black text-slate-900 tracking-tight mb-8 flex items-center gap-4">
                            <div className="w-12 h-12 bg-blue-50 text-blue-600 rounded-2xl flex items-center justify-center shadow-lg"><Database size={20} /></div>
                            Infrastructure & Localization
                        </h3>
                        <p className="text-slate-400 text-sm font-medium mb-6">
                            Hard Cap for total database connections across all tenants. Prevents Node.js from overwhelming the Postgres instance.
                        </p>

                        <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
                            <div>
                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1 mb-2 block">Global Connection Cap</label>
                                <div className="flex gap-4 items-center">
                                    <input
                                        type="number"
                                        min="10"
                                        value={dbConfig.max_connections || dbConfig.maxConnections || 10}
                                        onChange={(e) => setDbConfig({ ...dbConfig, max_connections: parseInt(e.target.value) })}
                                        className="w-full bg-slate-50 border border-slate-100 rounded-[1.8rem] py-5 px-8 text-sm font-bold text-slate-900 outline-none focus:ring-4 focus:ring-blue-500/10 transition-all text-center"
                                    />
                                </div>
                            </div>

                            <div>
                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1 mb-2 block">Project Timezone</label>
                                {/* IMMUTABLE TIMEZONE DISPLAY */}
                                <div className="w-full bg-slate-100 border border-slate-200 rounded-[1.8rem] py-5 px-8 flex items-center justify-between group cursor-not-allowed">
                                    <span className="text-sm font-bold text-slate-500">{timezone}</span>
                                    <Lock size={16} className="text-slate-400 group-hover:text-amber-500 transition-colors" />
                                </div>
                                <p className="text-[9px] text-slate-400 mt-2 px-2">
                                    Immutable. Defined at instance creation to ensure data integrity.
                                </p>
                            </div>
                        </div>

                        <div className="mt-8">
                            <button
                                onClick={() => handleUpdateSettings()}
                                disabled={saving}
                                className="bg-blue-600 text-white px-10 py-5 rounded-[1.8rem] font-black uppercase tracking-widest text-xs flex items-center justify-center hover:bg-blue-700 transition-all shadow-xl disabled:opacity-50 w-full md:w-auto"
                            >
                                {saving ? <Loader2 className="animate-spin" size={16} /> : 'Apply Configuration'}
                            </button>
                        </div>
                    </div>

                    {/* DOMAIN CONFIG */}
                    <div className="bg-white border border-slate-200 rounded-[4rem] p-12 shadow-sm relative">
                        {/* Isolated overflow container for background decorations */}
                        <div className="absolute inset-0 rounded-[4rem] overflow-hidden pointer-events-none">
                            <div className="absolute top-0 right-0 p-10 opacity-5"><Globe size={160} /></div>
                        </div>

                        <h3 className="text-2xl font-black text-slate-900 tracking-tight mb-8 flex items-center gap-4 relative z-10">
                            <div className="w-12 h-12 bg-emerald-50 text-emerald-600 rounded-2xl flex items-center justify-center shadow-lg"><Globe size={20} /></div> Custom Domain
                        </h3>

                        <div className="flex gap-4 items-center mb-6 relative z-10 domain-suggestions-container">
                            <div className="flex-1 relative">
                                <input
                                    value={displayDomain || customDomain}
                                    onChange={(e) => {
                                        const val = e.target.value;
                                        setDisplayDomain(val);
                                        setCustomDomain(unicodeToPunycode(val));
                                        if (!project?.custom_domain) setShowDomainSuggestions(true);
                                    }}
                                    onFocus={() => {
                                        if (!project?.custom_domain && availableCerts.length > 0) {
                                            setShowDomainSuggestions(true);
                                        }
                                    }}
                                    onBlur={() => {
                                        // Delay to allow clicking on a suggestion
                                        setTimeout(() => setShowDomainSuggestions(false), 200);
                                    }}
                                    placeholder="api.my-app.com"
                                    className={`w-full bg-slate-50 border ${project?.custom_domain ? 'border-emerald-200 text-emerald-800' : 'border-slate-100'} rounded-[1.8rem] py-5 px-8 text-sm font-bold text-slate-900 outline-none focus:ring-4 focus:ring-emerald-500/10 transition-all`}
                                    disabled={project?.custom_domain}
                                />
                                {/* DROPDOWN DE SUGESTÕES DE DOMÍNIOS INTELIGENTE */}
                                {showDomainSuggestions && domainSuggestions.length > 0 && !project?.custom_domain && (
                                    <div className="absolute top-full left-0 right-0 mt-2 bg-white border border-slate-200 rounded-[2rem] shadow-2xl z-[100] max-h-64 overflow-y-auto animate-in slide-in-from-top-2 duration-200">
                                        <div className="p-3">
                                            <div className="flex items-center justify-between px-3 py-2 mb-1 border-b border-slate-50">
                                                <p className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Sugestões Inteligentes</p>
                                                <span className="text-[9px] font-bold text-emerald-500 bg-emerald-50 px-2 py-0.5 rounded-full">{domainSuggestions.length}</span>
                                            </div>
                                            {domainSuggestions.map((suggestion: any, idx: number) => (
                                                <button
                                                    key={`${suggestion.value}-${idx}`}
                                                    onClick={() => {
                                                        setCustomDomain(suggestion.value);
                                                        setDisplayDomain(suggestion.label);
                                                        setShowDomainSuggestions(false);
                                                    }}
                                                    className="w-full text-left px-4 py-3 rounded-xl hover:bg-slate-50 transition-all flex items-center justify-between group"
                                                >
                                                    <div className="flex items-center gap-3">
                                                        <div className={`w-8 h-8 rounded-lg flex items-center justify-center transition-colors ${
                                                            suggestion.type === 'expansion' ? 'bg-emerald-500 text-white shadow-lg shadow-emerald-200' : 'bg-slate-100 text-slate-400 group-hover:bg-emerald-100 group-hover:text-emerald-600'
                                                        }`}>
                                                            {suggestion.type === 'expansion' ? <Zap size={14} /> : <Globe size={14} />}
                                                        </div>
                                                        <div className="flex flex-col">
                                                            <span className="text-sm font-bold text-slate-700 group-hover:text-slate-900 transition-colors">
                                                                {suggestion.label}
                                                            </span>
                                                            {suggestion.type === 'expansion' && (
                                                                <span className="text-[9px] text-slate-400 font-medium">Auto-completing wildcard: {suggestion.source}</span>
                                                            )}
                                                        </div>
                                                    </div>
                                                    {suggestion.type === 'wildcard' && (
                                                        <span className="text-[9px] bg-emerald-100 text-emerald-600 px-2 py-0.5 rounded-full font-bold">Wildcard</span>
                                                    )}
                                                    {suggestion.type === 'expansion' && (
                                                        <div className="opacity-0 group-hover:opacity-100 transition-opacity">
                                                            <Plus size={14} className="text-emerald-500" />
                                                        </div>
                                                    )}
                                                </button>
                                            ))}
                                        </div>
                                    </div>
                                )}
                            </div>
                            {project?.custom_domain ? (
                                <button onClick={handleDeleteDomainClick} className="bg-rose-50 text-rose-600 p-5 rounded-[1.8rem] hover:bg-rose-100 transition-all"><Trash2 size={20} /></button>
                            ) : (
                                <button onClick={handleSaveDomainClick} disabled={saving || !customDomain} className="bg-emerald-600 text-white px-8 py-5 rounded-[1.8rem] font-black uppercase tracking-widest text-xs shadow-xl hover:bg-emerald-700 transition-all disabled:opacity-50">{saving ? <Loader2 className="animate-spin" size={16} /> : 'Connect'}</button>
                            )}
                        </div>

                        {project?.custom_domain && (
                            <div className={`p-6 rounded-[2.5rem] flex items-center gap-4 relative z-10 ${bestCertMatch ? 'bg-emerald-50 border border-emerald-100' : 'bg-amber-50 border border-amber-100'}`}>
                                {bestCertMatch ? <CheckCircle2 size={24} className="text-emerald-500" /> : <AlertTriangle size={24} className="text-amber-500" />}
                                <div>
                                    <h4 className={`font-bold text-sm ${bestCertMatch ? 'text-emerald-900' : 'text-amber-900'}`}>{bestCertMatch ? 'SSL Certificate Active' : 'No Valid Certificate Found'}</h4>
                                    <p className="text-[10px] opacity-80 mt-1">{bestCertMatch ? `Secured by: ${bestCertMatch}` : 'Add a certificate matching this domain in System Settings > Vault.'}</p>
                                </div>
                            </div>
                        )}
                    </div>

                    {/* FIREBASE CONFIG */}
                    <div className="bg-white border border-slate-200 rounded-[4rem] p-12 shadow-sm relative overflow-hidden group">
                        <div className="absolute top-0 right-0 p-10 opacity-5 group-hover:scale-110 transition-transform"><CloudLightning size={160} /></div>
                        <h3 className="text-2xl font-black text-slate-900 tracking-tight mb-8 flex items-center gap-4">
                            <div className="w-12 h-12 bg-amber-50 text-amber-600 rounded-2xl flex items-center justify-center shadow-lg"><CloudLightning size={20} /></div> Mobile Push (FCM)
                        </h3>

                        <div className="space-y-6 relative z-10">
                            <p className="text-slate-400 text-sm font-medium">Paste your Firebase Service Account JSON to enable the Push Engine.</p>
                            {hasFirebase ? (
                                <div className="bg-emerald-50 border border-emerald-100 p-6 rounded-[2rem] flex items-center justify-between">
                                    <div className="flex items-center gap-4">
                                        <div className="w-10 h-10 bg-emerald-100 rounded-full flex items-center justify-center text-emerald-600"><CheckCircle2 size={20} /></div>
                                        <div><h4 className="font-bold text-emerald-900 text-sm">FCM Configured</h4><p className="text-[10px] text-emerald-700">Push Notifications are active.</p></div>
                                    </div>
                                    <button onClick={() => setHasFirebase(false)} className="text-[10px] font-bold text-emerald-600 hover:text-emerald-800 uppercase tracking-widest">Update</button>
                                </div>
                            ) : (
                                <>
                                    <textarea
                                        value={firebaseJson}
                                        onChange={(e) => setFirebaseJson(e.target.value)}
                                        className="w-full h-32 bg-slate-50 border border-slate-100 rounded-[1.8rem] p-6 text-xs font-mono text-slate-600 outline-none focus:ring-4 focus:ring-amber-500/10 resize-none"
                                        placeholder='{ "type": "service_account", "project_id": "..." }'
                                    />
                                    <button onClick={handleSaveFirebase} disabled={saving || !firebaseJson} className="bg-amber-500 text-white px-8 py-4 rounded-[1.8rem] font-black uppercase tracking-widest text-xs shadow-xl hover:bg-amber-600 transition-all disabled:opacity-50 w-full md:w-auto">
                                        {saving ? <Loader2 className="animate-spin" size={16} /> : 'Activate Push Engine'}
                                    </button>
                                </>
                            )}
                        </div>
                    </div>

                    {/* BYOD / EXTERNAL DB */}
                    <div className="bg-white border border-slate-200 rounded-[4rem] p-12 shadow-sm relative overflow-hidden">
                        <h3 className="text-2xl font-black text-slate-900 tracking-tight mb-8 flex items-center gap-4">
                            <div className="w-12 h-12 bg-indigo-50 text-indigo-600 rounded-2xl flex items-center justify-center shadow-lg"><Server size={20} /></div> Bring Your Own Database
                        </h3>

                        <div className="flex items-center gap-4 mb-6 p-4 bg-slate-50 rounded-[2rem]">
                            <span className="text-xs font-bold text-slate-500 uppercase tracking-widest flex-1">Eject from Managed DB</span>
                            <button
                                onClick={() => setIsEjected(!isEjected)}
                                className={`w-14 h-8 rounded-full p-1 transition-colors ${isEjected ? 'bg-indigo-600' : 'bg-slate-300'}`}
                            >
                                <div className={`w-6 h-6 bg-white rounded-full shadow-md transition-transform ${isEjected ? 'translate-x-6' : ''}`}></div>
                            </button>
                        </div>

                        {isEjected && (
                            <div className="space-y-6 animate-in slide-in-from-top-4">
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">External Connection String</label>
                                    <input
                                        value={externalDbUrl}
                                        onChange={(e: React.ChangeEvent<HTMLInputElement>) => setExternalDbUrl(e.target.value)}
                                        className="w-full bg-indigo-50/50 border border-indigo-100 rounded-[1.8rem] py-5 px-8 text-sm font-bold text-indigo-900 outline-none focus:ring-4 focus:ring-indigo-500/10"
                                        placeholder="postgres://user:pass@host:5432/db"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Read Replica (Optional)</label>
                                    <input
                                        value={readReplicaUrl}
                                        onChange={(e: React.ChangeEvent<HTMLInputElement>) => setReadReplicaUrl(e.target.value)}
                                        className="w-full bg-slate-50 border border-slate-100 rounded-[1.8rem] py-5 px-8 text-sm font-bold text-slate-900 outline-none focus:ring-4 focus:ring-indigo-500/10"
                                        placeholder="postgres://replica-host:5432/db"
                                    />
                                </div>
                                <button onClick={() => handleUpdateSettings()} disabled={saving} className="bg-indigo-600 text-white px-8 py-4 rounded-[1.8rem] font-black uppercase tracking-widest text-xs shadow-xl hover:bg-indigo-700 transition-all disabled:opacity-50 w-full md:w-auto">
                                    {saving ? <Loader2 className="animate-spin" size={16} /> : 'Connect & Migrate'}
                                </button>
                                <p className="text-[10px] text-indigo-400 font-bold px-2 mt-2">
                                    Note: Migration will install required extensions (pgcrypto, uuid-ossp) and setup schemas.
                                </p>
                            </div>
                        )}
                    </div>

                    {/* SECURITY (CORS & DISCOVERY) */}
                    <div className="bg-white border border-slate-200 rounded-[4rem] p-12 shadow-sm">
                        <h3 className="text-2xl font-black text-slate-900 tracking-tight mb-8 flex items-center gap-4">
                            <div className="w-12 h-12 bg-rose-50 text-rose-600 rounded-2xl flex items-center justify-center shadow-lg"><Shield size={20} /></div> Security Perimeter
                        </h3>

                        <div className="space-y-8">
                            {/* Schema Exposure Toggle */}
                            <div className="flex items-center justify-between p-4 bg-slate-50 rounded-[2rem] border border-slate-100">
                                <div>
                                    <h4 className="font-bold text-slate-900 text-sm">API Schema Discovery</h4>
                                    <p className="text-[10px] text-slate-400 font-medium">Expose OpenAPI/Swagger specs publicly for low-code tools.</p>
                                </div>
                                <button
                                    onClick={toggleSchemaExposure}
                                    className={`w-12 h-7 rounded-full p-1 transition-colors ${project?.metadata?.schema_exposure ? 'bg-emerald-500' : 'bg-slate-300'}`}
                                >
                                    <div className={`w-5 h-5 bg-white rounded-full shadow-md transition-transform ${project?.metadata?.schema_exposure ? 'translate-x-5' : ''}`}></div>
                                </button>
                            </div>

                            {/* Origins Manager */}
                            <div className="space-y-4">
                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Allowed Origins (CORS)</label>
                                <div className="flex gap-2">
                                    <input value={newOrigin} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setNewOrigin(e.target.value)} placeholder="https://myapp.com" className="flex-1 bg-slate-50 border border-slate-100 rounded-2xl py-3 px-6 text-xs font-bold outline-none" />
                                    <button onClick={addOrigin} className="bg-slate-900 text-white p-3 rounded-2xl hover:bg-slate-700 transition-all"><Plus size={16} /></button>
                                </div>
                                <div className="flex flex-wrap gap-2">
                                    {origins.length === 0 && <span className="text-xs text-slate-300 font-medium italic p-2">All origins allowed (Dev Mode)</span>}
                                    {origins.map((o: any, i: number) => (
                                        <div key={i} className="flex items-center gap-2 bg-white border border-slate-200 px-3 py-1.5 rounded-xl text-xs font-bold text-slate-600 shadow-sm">
                                            {o.url}
                                            <button onClick={() => removeOrigin(o.url)} className="text-rose-400 hover:text-rose-600"><X size={12} /></button>
                                        </div>
                                    ))}
                                </div>
                            </div>

                            {/* MCP Security Perimeter (Enterprise Upgrade) */}
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-8 pt-8 border-t border-slate-100">
                                {/* MCP Allowed IPs */}
                                <div className="space-y-4">
                                    <div className="flex items-center gap-2 mb-2">
                                        <div className="w-8 h-8 bg-indigo-50 text-indigo-600 rounded-lg flex items-center justify-center">
                                            <Shield size={16} />
                                        </div>
                                        <div>
                                            <h4 className="text-sm font-black text-slate-900 tracking-tight">MCP Allowed IPs</h4>
                                            <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">Whitelist for AI Agents (IPv4/IPv6)</p>
                                        </div>
                                    </div>
                                    <div className="flex gap-2">
                                        <input
                                            value={newMcpIp}
                                            onChange={(e: React.ChangeEvent<HTMLInputElement>) => setNewMcpIp(e.target.value)}
                                            onKeyDown={(e: React.KeyboardEvent<HTMLInputElement>) => e.key === 'Enter' && (setMcpIps([...mcpIps, newMcpIp]), setNewMcpIp(''))}
                                            placeholder="e.g. 192.168.1.1/32"
                                            className="flex-1 bg-slate-50 border border-slate-200 rounded-xl px-4 py-2.5 text-xs font-bold outline-none"
                                        />
                                        <button onClick={() => { if (newMcpIp) { setMcpIps([...mcpIps, newMcpIp]); setNewMcpIp(''); } }} className="bg-slate-900 text-white px-4 rounded-xl text-[10px] font-black uppercase tracking-widest hover:bg-indigo-600 transition-colors">Add</button>
                                    </div>
                                    <div className="flex flex-wrap gap-2">
                                        {mcpIps.length === 0 && <p className="text-[10px] text-slate-400 font-medium italic">Empty: Access from any IP is permitted (Default)</p>}
                                        {mcpIps.map((ip: string) => (
                                            <div key={ip} className="flex items-center gap-2 bg-slate-100 text-slate-600 px-3 py-1.5 rounded-lg text-[10px] font-bold group">
                                                {ip}
                                                <button onClick={() => setMcpIps(mcpIps.filter(i => i !== ip))} className="text-slate-300 hover:text-rose-500 transition-colors"><X size={12} /></button>
                                            </div>
                                        ))}
                                    </div>
                                </div>

                                {/* MCP Allowed Domains/URLs */}
                                <div className="space-y-4">
                                    <div className="flex items-center gap-2 mb-2">
                                        <div className="w-8 h-8 bg-indigo-50 text-indigo-600 rounded-lg flex items-center justify-center">
                                            <Globe size={16} />
                                        </div>
                                        <div>
                                            <h4 className="text-sm font-black text-slate-900 tracking-tight">MCP Allowed Origins</h4>
                                            <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">Origin filtering for MCP clients</p>
                                        </div>
                                    </div>
                                    <div className="flex gap-2">
                                        <input
                                            value={newMcpUrl}
                                            onChange={(e: React.ChangeEvent<HTMLInputElement>) => setNewMcpUrl(e.target.value)}
                                            onKeyDown={(e: React.KeyboardEvent<HTMLInputElement>) => e.key === 'Enter' && (setMcpUrls([...mcpUrls, newMcpUrl]), setNewMcpUrl(''))}
                                            placeholder="e.g. app.n8n.cloud"
                                            className="flex-1 bg-slate-50 border border-slate-200 rounded-xl px-4 py-2.5 text-xs font-bold outline-none"
                                        />
                                        <button onClick={() => { if (newMcpUrl) { setMcpUrls([...mcpUrls, newMcpUrl]); setNewMcpUrl(''); } }} className="bg-slate-900 text-white px-4 rounded-xl text-[10px] font-black uppercase tracking-widest hover:bg-indigo-600 transition-colors">Add</button>
                                    </div>
                                    <div className="flex flex-wrap gap-2">
                                        {mcpUrls.length === 0 && <p className="text-[10px] text-slate-400 font-medium italic">Empty: All origins allowed (Default)</p>}
                                        {mcpUrls.map((url: string) => (
                                            <div key={url} className="flex items-center gap-2 bg-slate-100 text-slate-600 px-3 py-1.5 rounded-lg text-[10px] font-bold">
                                                {url}
                                                <button onClick={() => setMcpUrls(mcpUrls.filter(u => u !== url))} className="text-slate-300 hover:text-rose-500 transition-colors"><X size={12} /></button>
                                            </div>
                                        ))}
                                    </div>
                                </div>

                                {/* MCP Data Volume Limit */}
                                <div className="space-y-4 md:col-span-2 bg-slate-50 p-6 rounded-2xl border border-slate-100">
                                    <div className="flex items-center justify-between">
                                        <div className="flex items-center gap-3">
                                            <div className="w-10 h-10 bg-indigo-600 text-white rounded-xl flex items-center justify-center shadow-lg shadow-indigo-200">
                                                <Layers size={20} />
                                            </div>
                                            <div>
                                                <h4 className="text-sm font-black text-slate-900 tracking-tight">MCP Data Volume Protection</h4>
                                                <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">Maximum rows per AI tool call (non-admin)</p>
                                            </div>
                                        </div>
                                        <div className="flex items-center gap-4">
                                            <input
                                                type="range"
                                                min="10"
                                                max="1000"
                                                step="10"
                                                value={mcpMaxRows}
                                                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setMcpMaxRows(parseInt(e.target.value))}
                                                className="w-32 accent-indigo-600"
                                            />
                                            <span className="text-xs font-black text-indigo-600 w-12 text-center">{mcpMaxRows}</span>
                                        </div>
                                    </div>
                                    <p className="text-[10px] text-slate-500 font-medium leading-relaxed">
                                        Restricts the volume of data an AI agent can retrieve in a single <code>run_sql</code> operation. 
                                        Admins (Service Role) are exempt from this limit. 
                                        Critical for preventing massive data exfiltration through prompt injections.
                                    </p>
                                </div>
                            </div>
                        </div>
                    </div>

                </div>
            </div>

            {/* Verify Password Modal */}
            {showVerifyModal && (
                <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[800] flex items-center justify-center p-8 animate-in zoom-in-95">
                    <div className="bg-white rounded-[3rem] p-10 max-w-sm w-full shadow-2xl text-center border border-slate-200">
                        <Lock size={40} className="mx-auto text-slate-900 mb-6" />
                        <h3 className="text-xl font-black text-slate-900 mb-2">Confirmação de Segurança</h3>
                        <p className="text-xs text-slate-500 font-bold mb-8">Digite sua senha atual para autorizar esta alteração crítica.</p>
                        <form onSubmit={handleVerifyAndExecute}>
                            <input
                                type="password"
                                autoFocus
                                value={verifyPassword}
                                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setVerifyPassword(e.target.value)}
                                className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-4 px-6 text-center font-bold text-slate-900 outline-none mb-6 focus:ring-4 focus:ring-indigo-500/10"
                                placeholder="••••••••"
                            />
                            <button type="submit" disabled={verifyLoading} className="w-full bg-slate-900 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-600 transition-all flex items-center justify-center gap-2">
                                {verifyLoading ? <Loader2 className="animate-spin" size={16} /> : 'Confirmar Acesso'}
                            </button>
                        </form>
                        <button onClick={() => { setShowVerifyModal(false); setPendingIntent(null); }} className="mt-4 text-xs font-bold text-slate-400 hover:text-slate-600">Cancelar</button>
                    </div>
                </div>
            )}

            {/* MFA Confirmation Modal (Downgrade/Connect MFA Protection) */}
            {mfaConfirm && (
                <div className="fixed inset-0 bg-slate-950/85 backdrop-blur-md z-[900] flex items-center justify-center p-8 animate-in zoom-in-95 duration-200">
                    <div className="bg-white border border-slate-200 rounded-[3rem] p-10 max-w-sm w-full shadow-2xl relative text-center">
                        <div className="w-16 h-16 bg-rose-100 text-rose-600 rounded-2xl flex items-center justify-center mx-auto mb-6 shadow-inner animate-pulse">
                            <Shield size={32} />
                        </div>
                        <h3 className="text-xl font-black text-slate-900 mb-2">Confirmação de Segurança</h3>
                        <p className="text-[11px] text-slate-500 font-medium mb-6 leading-relaxed">
                            Esta ação requer confirmação do administrador. Insira sua senha e o token OTP se estiver configurado.
                        </p>

                        <div className="space-y-4 mb-6 text-left">
                            <div>
                                <label className="text-[9px] font-black text-slate-400 uppercase tracking-widest ml-1 block mb-2">Senha do Administrador</label>
                                <input
                                    type="password"
                                    autoFocus
                                    value={mfaPassword}
                                    onChange={(e) => setMfaPassword(e.target.value)}
                                    placeholder="Digite sua senha"
                                    className="w-full bg-slate-50 border border-slate-200 rounded-[1.5rem] py-3.5 px-5 text-xs font-bold text-slate-900 outline-none focus:ring-4 focus:ring-rose-500/10 transition-all"
                                />
                            </div>

                            {mfaConfirm.otpRequired && (
                                <div>
                                    <label className="text-[9px] font-black text-slate-400 uppercase tracking-widest ml-1 block mb-2">Token OTP de 6 Dígitos</label>
                                    <input
                                        type="text"
                                        maxLength={6}
                                        value={mfaOtp}
                                        onChange={(e) => setMfaOtp(e.target.value.replace(/\D/g, ''))}
                                        placeholder="000000"
                                        className="w-full bg-slate-50 border border-slate-200 rounded-[1.5rem] py-3.5 px-5 text-center font-mono font-black text-sm tracking-widest text-slate-900 outline-none focus:ring-4 focus:ring-rose-500/10 transition-all"
                                    />
                                </div>
                            )}
                        </div>

                        <div className="flex gap-4">
                            <button
                                onClick={() => mfaConfirm.onCancel()}
                                className="flex-1 py-3 text-xs font-black text-slate-400 hover:text-slate-600 uppercase tracking-widest transition-colors"
                            >
                                Cancelar
                            </button>
                            <button
                                onClick={() => mfaConfirm.onSubmit(mfaPassword, mfaOtp)}
                                disabled={saving || !mfaPassword || (mfaConfirm.otpRequired && mfaOtp.length < 6)}
                                className="flex-[2] py-3.5 bg-rose-600 hover:bg-rose-700 text-white rounded-[1.5rem] text-xs font-black uppercase tracking-widest shadow-xl shadow-rose-600/20 hover:shadow-rose-600/40 transition-all flex justify-center items-center gap-2 disabled:opacity-50"
                            >
                                {saving ? <Loader2 className="animate-spin" size={16} /> : 'Confirmar & Aplicar'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            <ProjectVaultModal
                projectId={projectId}
                open={showVault}
                onClose={() => setShowVault(false)}
                onSuccess={(message) => {
                    setSuccess(message);
                    setTimeout(() => setSuccess(null), 2500);
                }}
                onError={(message) => alert(message)}
            />

        </div>
    );
};

const KeyControl: React.FC<{ label: string, value: string, isSecret: boolean, isRevealed: boolean, onReveal?: () => void, onRotate?: () => void, onCopy: () => void }> = ({ label, value, isSecret, isRevealed, onReveal, onRotate, onCopy }) => (
    <div className="group">
        <div className="flex justify-between items-center mb-2">
            <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">{label}</label>
            <div className="flex gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                {isSecret && (
                    <button onClick={onReveal} className="text-slate-400 hover:text-indigo-600 transition-colors" title={isRevealed ? "Hide" : "Reveal"}>
                        {isRevealed ? <EyeOff size={14} /> : <Eye size={14} />}
                    </button>
                )}
                {onRotate && (
                    <button onClick={onRotate} className="text-slate-400 hover:text-amber-600 transition-colors" title="Rotate Key">
                        <RefreshCw size={14} />
                    </button>
                )}
            </div>
        </div>
        <div className="flex items-center bg-slate-50 border border-slate-100 rounded-2xl p-1 relative overflow-hidden group/input">
            <code className="flex-1 bg-transparent px-4 py-3 font-mono text-xs text-slate-600 truncate font-bold">
                {value}
            </code>
            <button onClick={onCopy} className="p-3 bg-white text-slate-400 hover:text-indigo-600 rounded-xl shadow-sm hover:shadow-md transition-all">
                <Copy size={16} />
            </button>
        </div>
    </div>
);

export default ProjectSettings;
