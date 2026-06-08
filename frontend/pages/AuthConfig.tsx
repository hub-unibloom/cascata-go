
import React, { useState, useEffect, useCallback, useMemo } from 'react';
import {
    Users, Key, Shield, Plus, Search, Fingerprint, Mail, Smartphone,
    Globe, Trash2, Copy, CheckCircle2, AlertCircle, Loader2, X,
    UserPlus, CreditCard, Hash, Settings, Eye, EyeOff, Lock, Ban,
    Filter, ChevronLeft, ChevronRight, CheckSquare, Square, Link,
    Clock, Zap, Github, Facebook, Twitter, Edit2, Unlink, Layers,
    RefreshCcw, ArrowRight, LayoutTemplate, Send, ShieldAlert, Target,
    MessageSquare, Server, Plug, BellRing, PartyPopper, Code, Edit
} from 'lucide-react';

const AuthConfig: React.FC<{ projectId: string, currentEnv?: string }> = ({ projectId, currentEnv }) => {
    const [activeSection, setActiveSection] = useState<'users' | 'strategies' | 'orchestration' | 'messaging' | 'security' | 'apps' | 'schema'>('users');

    const getDataUrl = useCallback((path: string) => {
        const hash = typeof window !== 'undefined' ? window.location.hash : '';
        const match = hash.match(/\/branch\/([^/?]+)/);
        const activeBranch = (match && match[1]) ? match[1] : (currentEnv || 'live');

        if (activeBranch !== 'live') {
            return `/api/data/${projectId}/branch/${activeBranch}${path}`;
        }
        return `/api/data/${projectId}${path}`;
    }, [projectId, currentEnv]);

    // DIRECTORY STATE
    const [users, setUsers] = useState<any[]>([]);
    const [loadingUsers, setLoadingUsers] = useState(true);
    const [isSensitiveVisible, setIsSensitiveVisible] = useState(false);
    const [showVerifyModal, setShowVerifyModal] = useState(false);
    const [verifyPassword, setVerifyPassword] = useState('');
    const [searchQuery, setSearchQuery] = useState('');
    const [sortBy, setSortBy] = useState<'date' | 'alpha'>('date');
    const [page, setPage] = useState(1);
    const [pageSize, setPageSize] = useState(10);

    // USER DETAIL MODAL
    const [selectedUser, setSelectedUser] = useState<any>(null);
    const [showUserModal, setShowUserModal] = useState(false);
    const [deleteConfirmUuid, setDeleteConfirmUuid] = useState('');
    const [showDeleteModal, setShowDeleteModal] = useState<any>(null);
    const [activeSessions, setActiveSessions] = useState<any[]>([]);
    const [loadingSessions, setLoadingSessions] = useState(false);

    // LINK IDENTITY STATE
    const [showLinkIdentity, setShowLinkIdentity] = useState(false);
    const [linkIdentityForm, setLinkIdentityForm] = useState({ provider: 'email', identifier: '', password: '' });
    const [automations, setAutomations] = useState<any[]>([]);

    // CONFIGURATION STATE
    const [strategies, setStrategies] = useState<any>({});
    const [globalOrigins, setGlobalOrigins] = useState<string[]>([]);
    const [siteUrl, setSiteUrl] = useState(''); // Default Redirect
    const [selectedStrategy, setSelectedStrategy] = useState<string | null>(null);
    const [strategyConfig, setStrategyConfig] = useState<any>(null);
    const [editingStrategyName, setEditingStrategyName] = useState('');
    const [showConfigModal, setShowConfigModal] = useState(false);

    // SECURITY / SMART LOCKOUT STATE
    const [securityConfig, setSecurityConfig] = useState({
        max_attempts: 5,
        lockout_minutes: 15,
        strategy: 'hybrid' // 'ip' | 'identifier' | 'hybrid' | 'email'
    });

    // EMAIL CENTER STATE (New Architecture)
    const [emailTab, setEmailTab] = useState<'gateway' | 'templates' | 'library' | 'policies'>('gateway');

    // 1. Gateway Config (SMTP/Resend)
    const [emailGateway, setEmailGateway] = useState<any>({
        delivery_method: 'resend', // 'smtp' | 'resend' | 'webhook'
        from_email: 'noreply@cascata.io',
        resend_api_key: '',
        smtp_host: '',
        smtp_port: 587,
        smtp_user: '',
        smtp_pass: '',
        smtp_secure: false,
        webhook_url: ''
    });

    // 2. Templates Config
    const [emailTemplates, setEmailTemplates] = useState<any>({
        confirmation: { subject: 'Confirm Your Email', body: '<h2>Confirm your email</h2><p>Click the link below to confirm your email address:</p><p><a href="{{ .ConfirmationURL }}">Confirm Email</a></p>' },
        recovery: { subject: 'Reset Your Password', body: '<h2>Reset Password</h2><p>Click here to reset your password:</p><a href="{{ .ConfirmationURL }}">Reset Password</a>' },
        magic_link: { subject: 'Your Login Link', body: '<h2>Login Request</h2><p>Click here to login:</p><a href="{{ .ConfirmationURL }}">Sign In</a>' },
        login_alert: { subject: 'New Login Detected', body: '<h2>New Login</h2><p>We detected a new login to your account at {{ .Date }}.</p>' },
        welcome_email: { subject: 'Welcome!', body: '<h2>Welcome to our platform!</h2><p>We are glad to have you with us.</p>' }
    });
    const [activeTemplateTab, setActiveTemplateTab] = useState<'confirmation' | 'recovery' | 'magic_link' | 'login_alert' | 'welcome_email'>('confirmation');

    // 2.5 New Messaging Templates Library
    const [messagingTemplates, setMessagingTemplates] = useState<any>({});
    const [selectedLibraryTemplate, setSelectedLibraryTemplate] = useState<string | null>(null);
    const [editingTemplate, setEditingTemplate] = useState<string | null>(null);
    const [editingVariantLang, setEditingVariantLang] = useState<string>('en-US');
    const [showCreateTemplateModal, setShowCreateTemplateModal] = useState(false);
    const [newTemplateForm, setNewTemplateForm] = useState({ name: '', type: 'otp_challenge', default_language: 'en-US' });

    // 3. Policies Config
    const [emailPolicies, setEmailPolicies] = useState({
        email_confirmation: false,
        disable_magic_link: false,
        send_welcome_email: false,
        send_login_alert: false,
        notify_new_biometric_device: false,
        login_webhook_url: ''
    });

    // PROVIDER CONFIG
    const [providerConfig, setProviderConfig] = useState<any>({ client_id: '', client_secret: '' });
    const [showProviderConfig, setShowProviderConfig] = useState<string | null>(null);

    // LINKED TABLES (Concatenation)
    const [availableTables, setAvailableTables] = useState<string[]>([]);
    const [linkedTables, setLinkedTables] = useState<string[]>([]);
    const [projectDomain, setProjectDomain] = useState<string>('');

    // SCHEMA & TABLES FOR APP CLIENT
    const [availableSchemas, setAvailableSchemas] = useState<string[]>(['public']);
    const [selectedSchema, setSelectedSchema] = useState<string>('public');
    const [tablesBySchema, setTablesBySchema] = useState<Record<string, string[]>>({ public: [] });

    // SCHEMA FOR SCHEMA CONCATENATION SECTION
    const [selectedSchemaForLinking, setSelectedSchemaForLinking] = useState<string>('public');

    // CUSTOM STRATEGY STATE
    const [newStrategyName, setNewStrategyName] = useState('');
    const [showNewStrategy, setShowNewStrategy] = useState(false);

    // GENERAL
    const [executing, setExecuting] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [success, setSuccess] = useState<string | null>(null);

    // APP CLIENTS (Multi-App Identities)
    const [appClients, setAppClients] = useState<any[]>([]);
    const [showAppClientModal, setShowAppClientModal] = useState(false);
    const [newAppClientConfig, setNewAppClientConfig] = useState({ 
        name: '', 
        site_url: '', 
        allowed_origins: '',
        allowed_tables: [] as string[],  // Format: "schema.table" or just "table" for public
        blocked_tables: [] as string[]   // Format: "schema.table" or just "table" for public
    });
    
    // APP CLIENT SECURITY (Update/Rotate/Delete with password confirmation)
    const [showConfirmModal, setShowConfirmModal] = useState(false);
    const [confirmAction, setConfirmAction] = useState<'update' | 'rotate' | 'delete' | null>(null);
    const [confirmClientId, setConfirmClientId] = useState<string | null>(null);
    const [confirmPassword, setConfirmPassword] = useState('');
    const [rotatedKey, setRotatedKey] = useState<string | null>(null);
    
    // APP CLIENT EDIT STATE
    const [editingClientId, setEditingClientId] = useState<string | null>(null);
    const [editSiteUrl, setEditSiteUrl] = useState('');
    const [editAllowedOrigins, setEditAllowedOrigins] = useState('');
    const [editAllowedTables, setEditAllowedTables] = useState<string[]>([]);
    const [editBlockedTables, setEditBlockedTables] = useState<string[]>([]);
    // CREATE USER STATE (Independent)
    const [showCreateUser, setShowCreateUser] = useState(false);
    const [createUserForm, setCreateUserForm] = useState({ identifier: '', password: '', provider: 'email' });

    // ORCHESTRATION / SOVEREIGN POLICIES
    const [policies, setPolicies] = useState<any[]>([]);
    const [loadingPolicies, setLoadingPolicies] = useState(false);
    const [showPolicyModal, setShowPolicyModal] = useState(false);
    const [editingPolicy, setEditingPolicy] = useState<any>(null);
    const [policyForm, setPolicyForm] = useState({
        name: '', priority: 0, provider: '*', origin: '*',
        require_password: true, require_otp: false,
        require_user_mfa_choice: false, auto_login: false, active: true
    });

    // PANIC / EMERGENCY RECOVOCATION
    const [showPanicModal, setShowPanicModal] = useState(false);
    const [panicForm, setPanicForm] = useState({ target_type: 'user', target_value: '', reason: 'Sovereign Manual Intervention' });

    // UTILS
    const safeCopy = (text: string) => {
        try {
            const textArea = document.createElement("textarea");
            textArea.value = text;
            textArea.style.position = "fixed";
            textArea.style.left = "-9999px";
            document.body.appendChild(textArea);
            textArea.select();
            document.execCommand('copy');
            document.body.removeChild(textArea);
            setSuccess("Copiado para área de transferência.");
            setTimeout(() => setSuccess(null), 2000);
        } catch (err) {
            setError("Erro ao copiar.");
        }
    };

    const isValidUrl = (str: string) => {
        if (str === '*' || str.startsWith('*.')) return true;
        try { new URL(str.includes('://') ? str : `https://${str}`); return true; } catch { return false; }
    };

    const formatProviderName = (provider: string) => {
        const labels: Record<string, string> = {
            totp: 'TOTP',
            biometria: 'Biometria',
            email_otp: 'Email OTP',
            sms_otp: 'SMS OTP',
            whatsapp_custom: 'WhatsApp Custom',
            google: 'Google',
            github: 'GitHub',
            email: 'Email',
            phone: 'Phone'
        };
        return labels[provider] || provider.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
    };

    const getIdentityIcon = (provider: string) => {
        if (provider === 'email' || provider === 'email_otp') return <Mail size={16} />;
        if (provider === 'phone' || provider === 'sms_otp' || provider.includes('whatsapp')) return <Smartphone size={16} />;
        if (provider === 'totp') return <Clock size={16} />;
        if (provider === 'biometria' || provider.includes('passkey')) return <Fingerprint size={16} />;
        if (provider === 'github') return <Github size={16} />;
        return <Globe size={16} />;
    };

    // --- FETCHERS ---
    const fetchData = useCallback(async () => {
        setLoadingUsers(true);
        try {
            const token = localStorage.getItem('cascata_token');
            // Fetch users with higher limit to maintain list behavior
            const [usersRes, projRes, tablesRes, policiesRes, schemasRes, strategiesRes] = await Promise.all([
                fetch(getDataUrl('/auth/users?limit=1000'), { headers: { 'Authorization': `Bearer ${token}` } }),
                fetch('/api/control/projects', { headers: { 'Authorization': `Bearer ${token}` } }),
                fetch(getDataUrl('/tables?schema=public'), { headers: { 'Authorization': `Bearer ${token}` } }),
                fetch(getDataUrl('/auth/orchestration/policies'), { headers: { 'Authorization': `Bearer ${token}` } }),
                fetch(getDataUrl('/schemas'), { headers: { 'Authorization': `Bearer ${token}` } }).catch(() => null), // Optional endpoint
                fetch(getDataUrl('/auth/strategies'), { headers: { 'Authorization': `Bearer ${token}` } }).catch(() => null) // Optional strategies endpoint
            ]);

            if (!usersRes.ok || !projRes.ok || !tablesRes.ok) {
                throw new Error("Falha na comunicação com o servidor.");
            }

            const usersData = await usersRes.json();
            // Handle both legacy array and new paginated object structure
            // Also handle null/undefined responses gracefully
            let userList = [];
            if (usersData) {
                userList = Array.isArray(usersData) ? usersData : (usersData.data || []);
            }
            setUsers(userList);

            if (policiesRes.ok) {
                setPolicies(await policiesRes.json());
            }

            // Load schemas
            let schemas = ['public'];
            if (schemasRes && schemasRes.ok) {
                const schemasData = await schemasRes.json();
                if (Array.isArray(schemasData) && schemasData.length > 0) {
                    // API returns [{name: 'schema1'}, {name: 'schema2'}]
                    schemas = schemasData.map((s: any) => s.name || s);
                }
            }
            setAvailableSchemas(schemas);

            // Load tables for public schema initially
            const tablesData = await tablesRes.json();
            const tablesMap: Record<string, string[]> = { public: [] };
            const allTableNames: string[] = [];
            
            if (Array.isArray(tablesData)) {
                tablesData.forEach((t: any) => {
                    const tableName = t.name || t;
                    tablesMap['public'].push(tableName);
                    allTableNames.push(tableName);
                });
            }
            
            setTablesBySchema(tablesMap);
            setAvailableTables(allTableNames);

            const projects = await projRes.json();
            const currentProj = Array.isArray(projects) ? projects.find((p: any) => p.slug === projectId) : null;

            // Store Project Info
            setProjectDomain(currentProj?.custom_domain || '');
            // Use global_site_url from project level (fallback to metadata.auth_config.site_url for legacy)
            setSiteUrl(currentProj?.global_site_url || currentProj?.metadata?.auth_config?.site_url || currentProj?.metadata?.extra?.auth_config?.site_url || '');
            setAppClients(currentProj?.metadata?.app_clients || []);

            // Load Security Config
            const sec = (currentProj?.metadata?.auth_config || currentProj?.metadata?.extra?.auth_config || {}).security || {};
            setSecurityConfig({
                max_attempts: sec.max_attempts || 5,
                lockout_minutes: sec.lockout_minutes || 15,
                strategy: sec.strategy || 'hybrid'
            });

            // Load Email Gateway & Policies
            const authConfig = currentProj?.metadata?.auth_config || currentProj?.metadata?.extra?.auth_config || {};
            const strategyEmail = (currentProj?.metadata?.auth_strategies || currentProj?.metadata?.extra?.auth_strategies || {}).email || {};

            // Merge Gateway Config
            setEmailGateway(prev => ({
                ...prev,
                ...strategyEmail,
                delivery_methods: strategyEmail.delivery_methods || []
            }));

            // Merge Policies
            setEmailPolicies({
                email_confirmation: authConfig.email_confirmation || false,
                disable_magic_link: authConfig.disable_magic_link || false,
                send_welcome_email: authConfig.send_welcome_email || false,
                send_login_alert: authConfig.send_login_alert || false,
                notify_new_biometric_device: authConfig.notify_new_biometric_device || false,
                login_webhook_url: authConfig.login_webhook_url || ''
            });

            // Load Email Templates
            if (authConfig.email_templates) {
                setEmailTemplates((prev: any) => ({ ...prev, ...authConfig.email_templates }));
            }
            if (authConfig.messaging_templates) {
                setMessagingTemplates(authConfig.messaging_templates);
            }

            // Load Global Origins
            const rawOrigins = currentProj?.metadata?.allowed_origins || [];
            setGlobalOrigins(rawOrigins.map((o: any) => typeof o === 'string' ? o : o.url));

            // Load Strategies
            let savedStrategies = {};
            if (strategiesRes && strategiesRes.ok) {
                savedStrategies = await strategiesRes.json();
            } else {
                savedStrategies = currentProj?.metadata?.auth_strategies || currentProj?.metadata?.extra?.auth_strategies || {};
            }
            const defaultStrategies = {
                email: {
                    enabled: true,
                    rules: [],
                    jwt_expiration: '24h',
                    refresh_validity_days: 30,
                    password_enabled: true,
                    otp_enabled: true,
                    biometria_enabled: false
                },
                google: { enabled: false, rules: [], jwt_expiration: '24h', refresh_validity_days: 30 },
                github: { enabled: false, rules: [], jwt_expiration: '24h', refresh_validity_days: 30 }
            };
            setStrategies({ ...defaultStrategies, ...savedStrategies });

            // Load Linked Tables
            setLinkedTables(currentProj?.metadata?.extra?.linked_tables || []);

            // Fetch automations
            const automationsRes = await fetch(`/api/data/${projectId}/automations`, {
                headers: { 'Authorization': `Bearer ${token}` }
            }).catch(() => null);
            if (automationsRes && automationsRes.ok) {
                const autoData = await automationsRes.json();
                setAutomations(Array.isArray(autoData) ? autoData : (autoData.data || []));
            }

        } catch (e: any) {
            console.error("Fetch Error", e);
            setError(e.message || "Erro ao carregar dados.");
        } finally {
            setLoadingUsers(false);
        }
    }, [projectId]);

    useEffect(() => { fetchData(); }, [fetchData]);

    // Fetch tables when selected schema changes (lazy loading)
    useEffect(() => {
        if (!selectedSchema || tablesBySchema[selectedSchema]) {
            return; // Already loaded or no schema selected
        }
        
        const loadTablesForSchema = async () => {
            try {
                const token = localStorage.getItem('cascata_token');
                const res = await fetch(getDataUrl(`/tables?schema=${selectedSchema}`), {
                    headers: { 'Authorization': `Bearer ${token}` }
                });
                
                if (res.ok) {
                    const tablesData = await res.json();
                    const tableNames: string[] = [];
                    
                    if (Array.isArray(tablesData)) {
                        tablesData.forEach((t: any) => {
                            tableNames.push(t.name || t);
                        });
                    }
                    
                    setTablesBySchema(prev => ({
                        ...prev,
                        [selectedSchema]: tableNames
                    }));
                    setAvailableTables(prev => [...prev, ...tableNames]);
                }
            } catch (e) {
                console.error('Failed to load tables for schema:', selectedSchema, e);
            }
        };
        
        loadTablesForSchema();
    }, [selectedSchema, projectId, tablesBySchema]);

    // Fetch tables when schema for linking section changes (lazy loading)
    useEffect(() => {
        if (!selectedSchemaForLinking || tablesBySchema[selectedSchemaForLinking]) {
            return; // Already loaded or no schema selected
        }
        
        const loadTablesForLinkingSchema = async () => {
            try {
                const token = localStorage.getItem('cascata_token');
                const res = await fetch(getDataUrl(`/tables?schema=${selectedSchemaForLinking}`), {
                    headers: { 'Authorization': `Bearer ${token}` }
                });
                
                if (res.ok) {
                    const tablesData = await res.json();
                    const tableNames: string[] = [];
                    
                    if (Array.isArray(tablesData)) {
                        tablesData.forEach((t: any) => {
                            tableNames.push(t.name || t);
                        });
                    }
                    
                    setTablesBySchema(prev => ({
                        ...prev,
                        [selectedSchemaForLinking]: tableNames
                    }));
                    setAvailableTables(prev => [...prev, ...tableNames]);
                }
            } catch (e) {
                console.error('Failed to load tables for linking schema:', selectedSchemaForLinking, e);
            }
        };
        
        loadTablesForLinkingSchema();
    }, [selectedSchemaForLinking, projectId, tablesBySchema]);

    // --- ACTIONS ---
    const handleVerifyPassword = async () => {
        setExecuting(true);
        try {
            const res = await fetch('/api/control/auth/verify', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({ password: verifyPassword })
            });
            if (res.ok) {
                setIsSensitiveVisible(true);
                setShowVerifyModal(false);
                setVerifyPassword('');
            } else {
                setError("Senha incorreta.");
            }
        } catch (e) { setError("Erro na verificação."); }
        finally { setExecuting(false); }
    };

    const handleCreateUser = async () => {
        setExecuting(true);
        try {
            await fetch(getDataUrl('/auth/users'), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({
                    strategies: [{
                        provider: createUserForm.provider,
                        identifier: createUserForm.identifier,
                        password: createUserForm.password
                    }]
                })
            });
            setSuccess("Usuário criado com sucesso.");
            setShowCreateUser(false);
            setCreateUserForm({ identifier: '', password: '', provider: 'email' });
            fetchData();
        } catch (e) { setError("Erro ao criar usuário."); }
        finally { setExecuting(false); }
    };

    const saveStrategies = async (newStrategies: any, authConfig?: any, newLinkedTables?: string[], messagingTemplatesOverride?: any) => {
        setExecuting(true);
        try {
            const body: any = { authStrategies: newStrategies };
            if (authConfig) body.authConfig = authConfig;
            if (newLinkedTables) body.linked_tables = newLinkedTables;
            if (messagingTemplatesOverride) {
                if (!body.authConfig) body.authConfig = {};
                body.authConfig.messaging_templates = messagingTemplatesOverride;
            }

            // Optimistic Update
            setStrategies(newStrategies);
            if (newLinkedTables) setLinkedTables(newLinkedTables);

            // Merge Auth Config (Preserve existing providers/settings)
            const projRes = await fetch('/api/control/projects', { headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` } });
            const projects = await projRes.json();
            const currentProj = projects.find((p: any) => p.slug === projectId);
            const currentMetadata = currentProj?.metadata || {};

            let finalAuthConfig = currentMetadata.auth_config || currentMetadata.extra?.auth_config || {};
            if (authConfig) {
                // Merge All Top Level Keys
                finalAuthConfig = { ...finalAuthConfig, ...authConfig };
            }

            // Ensure messaging_templates is preserved if not explicitly overridden
            if (!messagingTemplatesOverride && Object.keys(messagingTemplates).length > 0) {
                finalAuthConfig.messaging_templates = messagingTemplates;
            } else if (messagingTemplatesOverride) {
                finalAuthConfig.messaging_templates = messagingTemplatesOverride;
            }

            body.authConfig = finalAuthConfig;

            await fetch(getDataUrl('/auth/link'), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify(body)
            });

            setSuccess("Configuração salva.");
            setTimeout(() => setSuccess(null), 2000);
        } catch (e) {
            setError("Falha ao salvar.");
            fetchData();
        }
        finally { setExecuting(false); }
    };

    const handleSaveStrategyConfig = () => {
        let updatedStrategies = { ...strategies };

        if (selectedStrategy && editingStrategyName && selectedStrategy !== editingStrategyName) {
            if (updatedStrategies[editingStrategyName]) {
                setError("Este nome de estratégia já existe.");
                return;
            }
            const config = updatedStrategies[selectedStrategy];
            delete updatedStrategies[selectedStrategy];
            updatedStrategies[editingStrategyName] = { ...config, ...strategyConfig };
        } else {
            updatedStrategies[selectedStrategy!] = strategyConfig;
        }

        saveStrategies(updatedStrategies);
        setShowConfigModal(false);
    };

    const handleSaveSiteUrl = () => {
        saveStrategies(strategies, { site_url: siteUrl });
    };

    // --- APP CLIENTS LOGIC ---
    const updateAppClientsMeta = async (newClients: any[]) => {
        setExecuting(true);
        try {
            const res = await fetch(`/api/control/projects/${projectId}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({ metadata: { app_clients: newClients } })
            });
            if (res.ok) {
                setAppClients(newClients);
                setSuccess("App Clients atualizados com sucesso.");
                setTimeout(() => setSuccess(null), 2000);
            } else {
                throw new Error("Falha ao salvar metadados.");
            }
        } catch (e: any) {
            setError(e.message);
        } finally {
            setExecuting(false);
        }
    };

    // UUID v4 generator compatível com todos os browsers
    const generateUUID = () => {
        return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
            const r = Math.random() * 16 | 0;
            const v = c === 'x' ? r : (r & 0x3 | 0x8);
            return v.toString(16);
        });
    };

    const handleSaveAppClient = async () => {
        if (!newAppClientConfig.name) return;
        setExecuting(true);
        try {
            // Use the proper backend endpoint to create app client
            const res = await fetch(`/api/control/projects/${projectId}/app-clients`, {
                method: 'POST',
                headers: { 
                    'Content-Type': 'application/json', 
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` 
                },
                body: JSON.stringify({
                    name: newAppClientConfig.name,
                    site_url: newAppClientConfig.site_url,
                    allowed_origins: newAppClientConfig.allowed_origins.split(',').map(s => s.trim()).filter(Boolean),
                    allowed_tables: newAppClientConfig.allowed_tables,
                    blocked_tables: newAppClientConfig.blocked_tables
                })
            });
            if (res.ok) {
                const data = await res.json();
                // Backend returns the generated anon_key - add to local state
                const newClient = {
                    id: data.id,
                    name: data.name,
                    anon_key: data.anon_key, // This is the HMAC-generated key from backend
                    site_url: data.site_url,
                    allowed_origins: data.allowed_origins,
                    active: data.active
                };
                setAppClients([...appClients, newClient]);
                setSuccess(`App Client created! Key: ${data.anon_key.substring(0, 20)}... (store securely)`);
                setShowAppClientModal(false);
                setNewAppClientConfig({ name: '', site_url: '', allowed_origins: '', allowed_tables: [], blocked_tables: [] });
            } else {
                const err = await res.json();
                setError(err.error || "Failed to create app client");
            }
        } catch (e: any) { 
            setError(e.message); 
        } finally { 
            setExecuting(false); 
        }
    };

    // Abre modal de confirmação para delete
    const requestDeleteAppClient = (clientId: string) => {
        setConfirmAction('delete');
        setConfirmClientId(clientId);
        setConfirmPassword('');
        setShowConfirmModal(true);
    };

    // Abre modal de confirmação para rotate
    const requestRotateAppClient = (clientId: string) => {
        setConfirmAction('rotate');
        setConfirmClientId(clientId);
        setConfirmPassword('');
        setRotatedKey(null);
        setShowConfirmModal(true);
    };

    // Executa delete após confirmação com senha
    const executeDeleteAppClient = async () => {
        if (!confirmClientId || !confirmPassword) return;
        
        setExecuting(true);
        try {
            // Primeiro verifica a senha (endpoint existente do cascata - requer auth)
            const verifyRes = await fetch('/api/control/auth/verify', {
                method: 'POST',
                headers: { 
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
                },
                body: JSON.stringify({ password: confirmPassword })
            });
            
            if (!verifyRes.ok) {
                const err = await verifyRes.json().catch(() => ({}));
                setError(err.error || "Password verification failed.");
                setExecuting(false);
                return;
            }

            const res = await fetch(`/api/control/projects/${projectId}/app-clients/${confirmClientId}`, {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            if (res.ok) {
                setAppClients(appClients.filter((c: any) => c.id !== confirmClientId));
                setSuccess("App Client revoked successfully");
                setShowConfirmModal(false);
            } else {
                const err = await res.json();
                setError(err.error || "Failed to delete app client");
            }
        } catch (e: any) {
            setError(e.message);
        } finally {
            setExecuting(false);
            setConfirmPassword('');
        }
    };

    // Executa rotate após confirmação com senha
    const executeRotateAppClient = async () => {
        if (!confirmClientId || !confirmPassword) return;
        
        setExecuting(true);
        try {
            // Verifica senha primeiro (endpoint existente do cascata - requer auth)
            const verifyRes = await fetch('/api/control/auth/verify', {
                method: 'POST',
                headers: { 
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
                },
                body: JSON.stringify({ password: confirmPassword })
            });
            
            if (!verifyRes.ok) {
                const err = await verifyRes.json().catch(() => ({}));
                setError(err.error || "Password verification failed.");
                setExecuting(false);
                return;
            }

            const res = await fetch(`/api/control/projects/${projectId}/app-clients/${confirmClientId}/rotate`, {
                method: 'POST',
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            if (res.ok) {
                const data = await res.json();
                // Atualiza o client na lista com a nova chave
                setAppClients(appClients.map((c: any) => 
                    c.id === confirmClientId ? { ...c, anon_key: data.anon_key } : c
                ));
                setRotatedKey(data.anon_key);
                setSuccess("Key rotated successfully! Store the new key securely.");
            } else {
                const err = await res.json();
                setError(err.error || "Failed to rotate key");
            }
        } catch (e: any) {
            setError(e.message);
        } finally {
            setExecuting(false);
            setConfirmPassword('');
        }
    };

    // Legacy - mantido para compatibilidade (agora chama o novo fluxo)
    const handleDeleteAppClient = (clientId: string) => {
        requestDeleteAppClient(clientId);
    };

    // Start editing App Client
    const startEditAppClient = (client: any) => {
        setEditingClientId(client.id);
        setEditSiteUrl(client.site_url || '');
        setEditAllowedOrigins((client.allowed_origins || []).join(', '));
        setEditAllowedTables(client.allowed_tables || []);
        setEditBlockedTables(client.blocked_tables || []);
    };

    // Cancel editing
    const cancelEditAppClient = () => {
        setEditingClientId(null);
        setEditSiteUrl('');
        setEditAllowedOrigins('');
        setEditAllowedTables([]);
        setEditBlockedTables([]);
    };

    // Request update with password confirmation
    const requestUpdateAppClient = (clientId: string) => {
        setConfirmAction('update');
        setConfirmClientId(clientId);
        setConfirmPassword('');
        setRotatedKey(null);
        setShowConfirmModal(true);
    };

    // Execute App Client update after password verification
    const executeUpdateAppClient = async () => {
        if (!confirmClientId || !confirmPassword) return;
        
        setExecuting(true);
        try {
            // Verify password first
            const verifyRes = await fetch('/api/control/auth/verify', {
                method: 'POST',
                headers: { 
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
                },
                body: JSON.stringify({ password: confirmPassword })
            });
            
            if (!verifyRes.ok) {
                const err = await verifyRes.json().catch(() => ({}));
                setError(err.error || "Password verification failed.");
                setExecuting(false);
                return;
            }

            const res = await fetch(`/api/control/projects/${projectId}/app-clients/${confirmClientId}`, {
                method: 'PUT',
                headers: { 
                    'Content-Type': 'application/json', 
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` 
                },
                body: JSON.stringify({
                    site_url: editSiteUrl,
                    allowed_origins: editAllowedOrigins.split(',').map((s: string) => s.trim()).filter(Boolean),
                    allowed_tables: editAllowedTables,
                    blocked_tables: editBlockedTables
                })
            });
            
            if (res.ok) {
                // Update local state
                setAppClients(appClients.map((c: any) => 
                    c.id === confirmClientId 
                        ? { ...c, site_url: editSiteUrl, allowed_origins: editAllowedOrigins.split(',').map((s: string) => s.trim()).filter(Boolean), allowed_tables: editAllowedTables, blocked_tables: editBlockedTables }
                        : c
                ));
                setSuccess("App Client updated successfully");
                setEditingClientId(null);
                setShowConfirmModal(false);
            } else {
                const err = await res.json();
                setError(err.error || "Failed to update App Client");
            }
        } catch (e: any) {
            setError(e.message);
        } finally {
            setExecuting(false);
            setConfirmPassword('');
        }
    };

    const handleSaveSecurity = () => {
        saveStrategies(strategies, { security: securityConfig });
    };

    const handleSavePolicy = async () => {
        setExecuting(true);
        try {
            const res = await fetch(getDataUrl('/auth/orchestration/policies'), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify(editingPolicy ? { ...policyForm, id: editingPolicy.id } : policyForm)
            });
            if (res.ok) {
                setSuccess("Sovereign Policy synchronized.");
                setShowPolicyModal(false);
                fetchData();
            } else {
                const err = await res.json();
                setError(err.error || "Failed to save policy");
            }
        } catch (e: any) { setError(e.message); }
        finally { setExecuting(false); }
    };

    const handleDeletePolicy = async (id: string) => {
        if (!confirm("Are you sure? This law will be permanently removed from the orchestrator.")) return;
        try {
            const res = await fetch(getDataUrl(`/auth/orchestration/policies/${id}`), {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            if (res.ok) {
                setSuccess("Policy revoked.");
                fetchData();
            }
        } catch (e: any) { setError(e.message); }
    };

    const handlePanic = async () => {
        if (!confirm("EMERGENCY: This will instantly invalidate target sessions. Proceed?")) return;
        setExecuting(true);
        try {
            const res = await fetch(getDataUrl('/auth/orchestration/panic'), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({ ...panicForm })
            });
            if (res.ok) {
                setSuccess("Panic signal broadcasted. Target neutralized.");
                setShowPanicModal(false);
                fetchData();
            }
        } catch (e: any) { setError(e.message); }
        finally { setExecuting(false); }
    };

    const handleSaveEmailCenter = () => {
        // 1. Update Strategy 'email' with Gateway Config
        const updatedStrategies = {
            ...strategies,
            email: {
                ...strategies.email,
                ...emailGateway
            }
        };

        // 2. Update Auth Config with Policies & Templates
        const updatedAuthConfig = {
            email_templates: emailTemplates,
            ...emailPolicies
        };

        saveStrategies(updatedStrategies, updatedAuthConfig);
    };

    const openProviderConfig = async (provider: string) => {
        setShowProviderConfig(provider);
        try {
            const projRes = await fetch('/api/control/projects', { headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` } });
            const projects = await projRes.json();
            const currentProj = projects.find((p: any) => p.slug === projectId);
            const conf = (currentProj?.metadata?.auth_config || currentProj?.metadata?.extra?.auth_config)?.providers?.[provider] || { client_id: '', client_secret: '', authorized_clients: '', skip_nonce: false };
            setProviderConfig(conf);
        } catch (e) { }
    };

    const handleSaveProviderConfig = () => {
        if (!showProviderConfig) return;
        // We pass just the specific provider update, assuming saveStrategies merges correctly
        saveStrategies(strategies, { providers: { [showProviderConfig]: providerConfig } });
        setShowProviderConfig(null);
    };

    const addRuleToStrategy = (origin: string) => {
        if (!isValidUrl(origin)) { alert("URL inválida."); return; }
        setStrategyConfig(prev => {
            if (!prev) return prev;
            const currentRules = prev.rules || [];
            if (currentRules.some((r: any) => r.origin === origin)) {
                return { ...prev, newRule: '' };
            }
            return {
                ...prev,
                rules: [...currentRules, {
                    origin,
                    require_password: true,
                    require_otp: false,
                    require_totp: false,
                    auto_login: false
                }],
                newRule: ''
            };
        });
    };

    const removeRuleFromStrategy = (origin: string) => {
        setStrategyConfig({
            ...strategyConfig,
            rules: (strategyConfig.rules || []).filter((r: any) => r.origin !== origin)
        });
    };

    const toggleLinkedTable = (tableName: string) => {
        const next = linkedTables.includes(tableName)
            ? linkedTables.filter(t => t !== tableName)
            : [...linkedTables, tableName];
        saveStrategies(strategies, null, next);
    };

    const handleBlockUser = async (user: any) => {
        try {
            await fetch(getDataUrl(`/auth/users/${user.id}/status`), {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
                body: JSON.stringify({ banned: !user.banned })
            });
            if (selectedUser && selectedUser.id === user.id) {
                setSelectedUser({ ...selectedUser, banned: !user.banned });
            }
            fetchData();
            setSuccess(user.banned ? "Usuário desbloqueado." : "Usuário bloqueado.");
        } catch (e) { setError("Erro ao alterar status."); }
    };

    const handleDeleteUser = async () => {
        if (deleteConfirmUuid !== showDeleteModal?.id) { setError("UUID incorreto."); return; }
        setExecuting(true);
        try {
            await fetch(getDataUrl(`/auth/users/${showDeleteModal.id}`), {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            setShowDeleteModal(null);
            setShowUserModal(false);
            setDeleteConfirmUuid('');
            fetchData();
            setSuccess("Usuário excluído permanentemente.");
        } catch (e) { setError("Erro ao excluir."); }
        finally { setExecuting(false); }
    };

    const handleLinkIdentity = async () => {
        if (!selectedUser) return;
        setExecuting(true);
        try {
            const res = await fetch(getDataUrl(`/auth/users/${selectedUser.id}/identities`), {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
                },
                body: JSON.stringify(linkIdentityForm)
            });

            if (!res.ok) {
                const err = await res.json();
                throw new Error(err.error || "Failed to link identity");
            }

            setSuccess("Nova identidade vinculada.");
            setShowLinkIdentity(false);
            setLinkIdentityForm({ provider: 'email', identifier: '', password: '' });

            // Refresh user data
            fetchData();
            setShowUserModal(false);

        } catch (e: any) {
            setError(e.message);
        } finally {
            setExecuting(false);
        }
    };

    const fetchActiveSessions = async (userId: string) => {
        setLoadingSessions(true);
        try {
            const res = await fetch(getDataUrl(`/auth/users/${userId}/sessions`), {
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            if (res.ok) {
                const data = await res.json();
                // Handle null/undefined responses gracefully
                setActiveSessions(Array.isArray(data) ? data : []);
            } else {
                setActiveSessions([]);
            }
        } catch (e) {
            console.error("Failed to fetch sessions");
            setActiveSessions([]);
        } finally {
            setLoadingSessions(false);
        }
    };

    const handleRevokeSession = async (sessionId: string) => {
        if (!confirm("Revogar esta sessão? O dispositivo será deslogado imediatamente.")) return;
        setExecuting(true);
        try {
            const res = await fetch(getDataUrl(`/auth/users/${selectedUser.id}/sessions/${sessionId}`), {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            if (res.ok) {
                setSuccess("Sessão revogada com sucesso.");
                fetchActiveSessions(selectedUser.id);
            } else {
                setError("Erro ao revogar sessão.");
            }
        } catch (e) {
            setError("Erro na conexão.");
        } finally {
            setExecuting(false);
        }
    };

    const handleRevokeOtherSessions = async () => {
        if (!confirm("Desconectar TODOS os outros dispositivos deste usuário?")) return;
        setExecuting(true);
        try {
            const res = await fetch(getDataUrl(`/auth/users/${selectedUser.id}/sessions`), {
                method: 'DELETE',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
                },
                body: JSON.stringify({ current_session_id: null }) // We are admin, we revoke all
            });
            if (res.ok) {
                setSuccess("Demais sessões revogadas com sucesso.");
                fetchActiveSessions(selectedUser.id);
            } else {
                setError("Erro ao revogar sessões.");
            }
        } catch (e) {
            setError("Erro na conexão.");
        } finally {
            setExecuting(false);
        }
    };

    const handleUnlinkIdentity = async (identityId: string) => {
        if (!confirm("Remover esta forma de acesso do usuário?")) return;
        setExecuting(true);
        try {
            const res = await fetch(getDataUrl(`/auth/users/${selectedUser.id}/strategies/${identityId}`), {
                method: 'DELETE',
                headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
            });
            if (!res.ok) throw new Error((await res.json()).error);

            setSuccess("Identidade removida.");
            fetchData();
            setShowUserModal(false);
        } catch (e: any) { setError(e.message); }
        finally { setExecuting(false); }
    };

    const toggleStrategy = async (key: string) => {
        const currentEnabled = strategies[key]?.enabled;
        const updatedStrategies = {
            ...strategies,
            [key]: { ...strategies[key], enabled: !currentEnabled }
        };
        await saveStrategies(updatedStrategies);
    };

    const handleCreateCustomStrategy = () => {
        if (!newStrategyName) return;
        const key = newStrategyName.toLowerCase().replace(/[^a-z0-9_]/g, '_');
        if (strategies[key]) { setError("Strategy já existe."); return; }

        const newStrategies = {
            ...strategies,
            [key]: {
                enabled: true,
                display_name: newStrategyName,
                factor_id: key,
                type: 'custom_challenge_provider',
                challenge_provider: true,
                rules: [],
                jwt_expiration: '24h',
                refresh_validity_days: 30,
                password_enabled: false,
                magiclink_enabled: false,
                otp_enabled: true,
                otp_config: { length: 6, charset: 'numeric' }
            }
        };
        saveStrategies(newStrategies);
        setNewStrategyName('');
        setShowNewStrategy(false);
    };

    const handleDeleteStrategy = (key: string) => {
        if (!confirm(`Excluir permanentemente a strategy "${key}"? Usuários que usam apenas este método perderão acesso.`)) return;
        const { [key]: deleted, ...rest } = strategies;
        saveStrategies(rest);
    };

    // --- TEMPLATE LIBRARY HANDLERS ---
    const handleCreateTemplate = () => {
        if (!newTemplateForm.name) return;
        const id = 'tpl_' + Math.random().toString(36).substr(2, 9);
        const newTpl = {
            id,
            name: newTemplateForm.name,
            type: newTemplateForm.type,
            default_language: newTemplateForm.default_language,
            variants: {
                [newTemplateForm.default_language]: { subject: '', body: '' }
            }
        };
        const updated = { ...messagingTemplates, [id]: newTpl };
        setMessagingTemplates(updated);
        setEditingTemplate(id);
        setEditingVariantLang(newTemplateForm.default_language);
        setShowCreateTemplateModal(false);
        setNewTemplateForm({ name: '', type: 'otp_challenge', default_language: 'en-US' });
    };

    const handleDeleteTemplate = (tplId: string) => {
        if (!confirm('Permanently delete this template and all its language variants?')) return;
        const { [tplId]: _, ...rest } = messagingTemplates;
        setMessagingTemplates(rest);
        if (editingTemplate === tplId) setEditingTemplate(null);
    };

    const handleUpdateVariant = (tplId: string, lang: string, field: 'subject' | 'body', value: string) => {
        setMessagingTemplates((prev: any) => ({
            ...prev,
            [tplId]: {
                ...prev[tplId],
                variants: {
                    ...prev[tplId].variants,
                    [lang]: { ...prev[tplId].variants[lang], [field]: value }
                }
            }
        }));
    };

    const handleAddVariant = (tplId: string, lang: string) => {
        if (!lang || messagingTemplates[tplId]?.variants?.[lang]) return;
        setMessagingTemplates((prev: any) => ({
            ...prev,
            [tplId]: {
                ...prev[tplId],
                variants: { ...prev[tplId].variants, [lang]: { subject: '', body: '' } }
            }
        }));
        setEditingVariantLang(lang);
    };

    const handleRemoveVariant = (tplId: string, lang: string) => {
        const tpl = messagingTemplates[tplId];
        if (!tpl || Object.keys(tpl.variants).length <= 1) {
            setError('A template must have at least one language variant.');
            return;
        }
        if (tpl.default_language === lang) {
            setError('Cannot remove the default language. Change the default first.');
            return;
        }
        const { [lang]: _, ...restVariants } = tpl.variants;
        setMessagingTemplates((prev: any) => ({
            ...prev,
            [tplId]: { ...prev[tplId], variants: restVariants }
        }));
        setEditingVariantLang(Object.keys(restVariants)[0]);
    };

    const handleSaveTemplateLibrary = () => {
        saveStrategies(strategies, undefined, undefined, messagingTemplates);
    };

    const filteredUsers = useMemo(() => {
        if (!Array.isArray(users)) return [];

        let list = users.filter(u =>
            u.id.includes(searchQuery) ||
            u.identities?.some((i: any) => i.identifier.toLowerCase().includes(searchQuery.toLowerCase()))
        );
        if (sortBy === 'alpha') {
            list.sort((a, b) => (a.identities?.[0]?.identifier || '').localeCompare(b.identities?.[0]?.identifier || ''));
        } else {
            list.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
        }
        return list;
    }, [users, searchQuery, sortBy]);

    const paginatedUsers = filteredUsers.slice((page - 1) * pageSize, page * pageSize);
    const totalPages = Math.max(1, Math.ceil(filteredUsers.length / pageSize));

    const isOauth = (s: string) => ['google', 'github', 'facebook', 'twitter'].includes(s);

    const isAggressiveSecurity = securityConfig.max_attempts < 3 || securityConfig.lockout_minutes > 60;

    // --- CALLBACK URL HELPER ---
    const getCallbackUrl = () => {
        const host = projectDomain || window.location.host;
        const protocol = projectDomain ? 'https' : window.location.protocol.replace(':', '');
        const prefix = projectDomain ? '' : (currentEnv && currentEnv !== 'live' ? `/api/data/${projectId}/branch/${currentEnv}` : `/api/data/${projectId}`);
        return `${protocol}://${host}${prefix}/auth/v1/callback`;
    };

    return (
        <div className="flex h-full bg-[#F8FAFC]">
            {(error || success) && (
                <div className={`fixed top-8 left-1/2 -translate-x-1/2 z-[600] px-6 py-4 rounded-full shadow-2xl flex items-center gap-3 animate-in slide-in-from-top-4 ${error ? 'bg-rose-600 text-white' : 'bg-emerald-600 text-white'}`}>
                    {error ? <AlertCircle size={18} /> : <CheckCircle2 size={18} />}
                    <span className="text-xs font-bold">{error || success}</span>
                    <button onClick={() => { setError(null); setSuccess(null); }}><X size={14} className="opacity-60 hover:opacity-100" /></button>
                </div>
            )}

            {/* SIDEBAR NAVIGATION */}
            <nav className="w-[260px] bg-white border-r border-slate-200 shrink-0 flex flex-col">
                <div className="p-6 border-b border-slate-100">
                    <div className="flex items-center gap-3">
                        <div className="w-11 h-11 bg-slate-900 text-white rounded-2xl flex items-center justify-center shadow-lg">
                            <Fingerprint size={22} />
                        </div>
                        <div>
                            <h2 className="text-lg font-black text-slate-900 tracking-tight leading-none">Auth</h2>
                            <p className="text-[9px] text-indigo-600 font-bold uppercase tracking-[0.15em] mt-0.5">Identity & Access</p>
                        </div>
                    </div>
                </div>
                <div className="flex-1 p-3 space-y-1 overflow-y-auto">
                    {[
                        { id: 'users' as const, icon: Users, label: 'Users', desc: 'Directory & Sessions', count: users.length },
                        { id: 'strategies' as const, icon: Key, label: 'Strategies', desc: 'Identity Providers', count: Object.keys(strategies).length },
                        { id: 'orchestration' as const, icon: Layers, label: 'Orchestration', desc: 'Sovereign Security Laws', count: policies.length },
                        { id: 'messaging' as const, icon: Send, label: 'Messaging', desc: 'Templates & Gateway' },
                        { id: 'security' as const, icon: ShieldAlert, label: 'Security', desc: 'Lockout & Panic' },
                        { id: 'apps' as const, icon: Plug, label: 'App Clients', desc: 'Keys & Origins', count: appClients.length },
                        { id: 'schema' as const, icon: Layers, label: 'Schema', desc: 'Table Linking' },
                    ].map(item => (
                        <button
                            key={item.id}
                            onClick={() => setActiveSection(item.id)}
                            className={`w-full flex items-center gap-3 px-4 py-3 rounded-2xl transition-all text-left group ${activeSection === item.id
                                ? 'bg-indigo-50 text-indigo-700'
                                : 'text-slate-500 hover:bg-slate-50 hover:text-slate-700'
                                }`}
                        >
                            <div className={`w-9 h-9 rounded-xl flex items-center justify-center shrink-0 transition-colors ${activeSection === item.id ? 'bg-indigo-600 text-white shadow-md' : 'bg-slate-100 text-slate-400 group-hover:bg-slate-200'
                                }`}>
                                <item.icon size={18} />
                            </div>
                            <div className="min-w-0 flex-1">
                                <div className="flex items-center justify-between">
                                    <span className={`text-xs font-black uppercase tracking-widest ${activeSection === item.id ? 'text-indigo-700' : ''}`}>{item.label}</span>
                                    {item.count !== undefined && item.count > 0 && (
                                        <span className={`text-[9px] font-bold px-1.5 py-0.5 rounded-md ${activeSection === item.id ? 'bg-indigo-200 text-indigo-700' : 'bg-slate-100 text-slate-400'}`}>{item.count}</span>
                                    )}
                                </div>
                                <p className="text-[9px] font-medium text-slate-400 truncate mt-0.5">{item.desc}</p>
                            </div>
                        </button>
                    ))}
                </div>
                <div className="p-4 border-t border-slate-100">
                    <div className="text-[8px] font-bold text-slate-300 uppercase tracking-widest text-center">Cascata Auth Engine</div>
                </div>
            </nav>

            {/* MAIN CONTENT */}
            <div className="flex-1 overflow-y-auto">
                {/* USERS SECTION */}
                {activeSection === 'users' && (
                    <div className="p-10">
                        <div className="mb-8">
                            <h2 className="text-3xl font-black text-slate-900 tracking-tighter">User Directory</h2>
                            <p className="text-xs text-slate-400 font-bold mt-1">Manage identities, sessions, and access across all strategies.</p>
                        </div>
                        <div className="flex justify-between items-center bg-white p-4 rounded-[2rem] shadow-sm border border-slate-100">
                            <div className="flex items-center gap-4">
                                <div className="relative group">
                                    <Search className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-400" size={16} />
                                    <input value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} placeholder="Search UUID, email..." className="pl-12 pr-6 py-3 bg-slate-50 border-none rounded-2xl text-xs font-bold outline-none w-64 focus:ring-2 focus:ring-indigo-500/20 transition-all" />
                                </div>
                                <div className="flex items-center gap-2 bg-slate-50 px-3 py-2 rounded-xl">
                                    <Filter size={14} className="text-slate-400" />
                                    <select value={sortBy} onChange={(e) => setSortBy(e.target.value as any)} className="bg-transparent text-xs font-bold text-slate-600 outline-none">
                                        <option value="date">Newest First</option>
                                        <option value="alpha">A-Z</option>
                                    </select>
                                </div>
                            </div>

                            <div className="flex items-center gap-4">
                                <button onClick={() => setShowCreateUser(true)} className="bg-indigo-600 text-white px-6 py-3 rounded-2xl text-[10px] font-black uppercase tracking-widest flex items-center gap-2 hover:bg-indigo-700 transition-all shadow-xl shadow-indigo-100">
                                    <UserPlus size={16} /> New User
                                </button>
                                <button onClick={() => isSensitiveVisible ? setIsSensitiveVisible(false) : setShowVerifyModal(true)} className={`flex items-center gap-2 px-4 py-3 rounded-2xl text-[10px] font-black uppercase tracking-widest transition-all ${isSensitiveVisible ? 'bg-amber-50 text-amber-600' : 'bg-slate-900 text-white'}`}>
                                    {isSensitiveVisible ? <><EyeOff size={14} /> Hide Data</> : <><Eye size={14} /> Reveal Data</>}
                                </button>
                            </div>
                        </div>

                        {loadingUsers ? (
                            <div className="py-20 flex justify-center"><Loader2 className="animate-spin text-indigo-600" size={32} /></div>
                        ) : (
                            <div className="space-y-4">
                                {paginatedUsers.length === 0 && <p className="text-center py-10 text-slate-400 font-bold text-xs uppercase">No users found</p>}
                                {paginatedUsers.map(u => (
                                    <div
                                        key={u.id}
                                        onClick={() => {
                                            setSelectedUser(u);
                                            setShowUserModal(true);
                                            fetchActiveSessions(u.id);
                                        }}
                                        className={`bg-white border ${u.banned ? 'border-rose-200 bg-rose-50/10' : 'border-slate-200'} rounded-[2.5rem] p-6 hover:shadow-xl transition-all group relative overflow-hidden cursor-pointer`}
                                    >
                                        {u.banned && <div className="absolute top-0 right-0 bg-rose-500 text-white text-[9px] font-black px-4 py-1 rounded-bl-xl uppercase tracking-widest">Banned</div>}
                                        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-6">
                                            <div className="flex items-center gap-6">
                                                <div className={`w-14 h-14 rounded-2xl overflow-hidden flex items-center justify-center text-white text-xl font-bold shadow-lg ${u.banned ? 'bg-rose-400' : 'bg-slate-900'}`}>
                                                    {(u.raw_user_meta_data?.avatar_url || u.raw_user_meta_data?.picture) ? (
                                                        <img src={u.raw_user_meta_data?.avatar_url || u.raw_user_meta_data?.picture} alt="Avatar" className="w-full h-full object-cover" />
                                                    ) : (
                                                        u.identities?.[0]?.identifier?.[0]?.toUpperCase() || <Users />
                                                    )}
                                                </div>
                                                <div>
                                                    <div className="flex items-center gap-2 mb-1">
                                                        <span className="text-[10px] font-black text-slate-400 uppercase tracking-widest">UUID</span>
                                                        <code className="text-[10px] font-mono text-slate-500 bg-slate-100 px-2 py-0.5 rounded">{u.id}</code>
                                                        <button onClick={(e) => { e.stopPropagation(); safeCopy(u.id); }} className="text-slate-300 hover:text-indigo-600"><Copy size={12} /></button>
                                                    </div>
                                                    <h4 className={`text-lg font-bold ${isSensitiveVisible ? 'text-slate-900' : 'text-slate-400 blur-[4px] select-none'} transition-all`}>
                                                        {u.identities?.[0]?.identifier || 'Unknown Identity'}
                                                    </h4>
                                                    <div className="flex gap-4 mt-1">
                                                        <p className="text-[10px] text-slate-400 font-bold">Created: {new Date(u.created_at).toLocaleDateString()}</p>
                                                        {u.identities?.some((i: any) => i.verified_at) ? (
                                                            <p className="text-[10px] text-emerald-500 font-bold flex items-center gap-1"><CheckCircle2 size={10} /> Verified</p>
                                                        ) : (
                                                            <p className="text-[10px] text-amber-500 font-bold flex items-center gap-1"><AlertCircle size={10} /> Unverified</p>
                                                        )}
                                                    </div>
                                                </div>
                                            </div>

                                            <div className="flex items-center gap-3">
                                                {u.identities?.map((id: any, idx: number) => (
                                                    <div key={idx} className="bg-slate-50 border border-slate-100 px-3 py-1.5 rounded-lg flex items-center gap-2">
                                                        <span className="text-[9px] font-black uppercase text-indigo-600">{id.provider}</span>
                                                        {id.verified_at ? (
                                                            <CheckCircle2 size={10} className="text-emerald-500" />
                                                        ) : (
                                                            <AlertCircle size={10} className="text-amber-400" />
                                                        )}
                                                    </div>
                                                ))}
                                                <div className="px-4 text-slate-300"><ChevronRight size={16} /></div>
                                            </div>
                                        </div>
                                    </div>
                                ))}
                            </div>
                        )}

                        <div className="flex justify-center items-center gap-6 pt-4">
                            <button disabled={page === 1} onClick={() => setPage(p => p - 1)} className="p-3 rounded-xl bg-white border border-slate-200 disabled:opacity-50 hover:bg-slate-50"><ChevronLeft size={16} /></button>
                            <span className="text-xs font-black text-slate-400 uppercase tracking-widest">Page {page} of {totalPages}</span>
                            <button disabled={page === totalPages} onClick={() => setPage(p => p + 1)} className="p-3 rounded-xl bg-white border border-slate-200 disabled:opacity-50 hover:bg-slate-50"><ChevronRight size={16} /></button>
                        </div>
                    </div>
                )}

                {/* APP CLIENTS SECTION */}
                {activeSection === 'apps' && (
                    <div className="p-10">
                        <div className="mb-8">
                            <h2 className="text-3xl font-black text-slate-900 tracking-tighter">App Clients</h2>
                            <p className="text-xs text-slate-400 font-bold mt-1">Identity-aware API keys and allowed origins per application.</p>
                        </div>
                        <div className="space-y-8">

                            {/* IDENTITY-AWARE APP CLIENTS */}
                            <div className="bg-white border border-slate-200 rounded-[3rem] p-12 shadow-sm">
                                <div className="flex flex-col md:flex-row md:items-center justify-between gap-6 mb-8">
                                    <div className="flex items-center gap-4">
                                        <div className="w-12 h-12 bg-indigo-50 text-indigo-600 rounded-2xl flex items-center justify-center"><Layers size={20} /></div>
                                        <div>
                                            <h3 className="text-2xl font-black text-slate-900 tracking-tight">App Clients</h3>
                                            <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-1">Identity-Aware Keys & Origins</p>
                                        </div>
                                    </div>
                                    <button onClick={() => { setExecuting(false); setNewAppClientConfig({ name: '', site_url: '', allowed_origins: '' }); setShowAppClientModal(true); }} className="bg-slate-900 text-white px-6 py-3 rounded-2xl text-[10px] font-black uppercase tracking-widest flex items-center justify-center gap-2 hover:bg-slate-800 transition-all shrink-0">
                                        <Plus size={16} /> New App Client
                                    </button>
                                </div>

                                <div className="space-y-6">
                                    {/* SPECIFIC CLIENTS */}
                                    {appClients.map((client: any) => (
                                        <div key={client.id} className="p-6 rounded-[2rem] border border-slate-200 bg-white shadow-sm flex flex-col gap-4 relative group">
                                            <div className="absolute top-6 right-6 flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                                                <button 
                                                    onClick={() => requestRotateAppClient(client.id)} 
                                                    className="text-slate-300 hover:text-amber-500 transition-colors bg-white p-2 rounded-lg shadow-sm"
                                                    title="Rotate Key (requires password)"
                                                >
                                                    <RefreshCcw size={16} />
                                                </button>
                                                <button 
                                                    onClick={() => requestDeleteAppClient(client.id)} 
                                                    className="text-slate-300 hover:text-rose-500 transition-colors bg-white p-2 rounded-lg shadow-sm"
                                                    title="Delete App Client (requires password)"
                                                >
                                                    <Trash2 size={16} />
                                                </button>
                                            </div>
                                            <div className="pr-20">
                                                <h4 className="font-bold text-slate-900">{client.name}</h4>
                                                <div className="flex items-center gap-2 mt-1">
                                                    <code className="text-[9px] bg-indigo-50 text-indigo-600 px-2 py-0.5 rounded font-mono">ID: {client.id}</code>
                                                    <span className="text-[9px] text-slate-400">(use in OAuth URL)</span>
                                                </div>
                                                <div className="flex items-center gap-2 mt-2">
                                                    <code className="text-[10px] bg-slate-100 text-slate-600 px-2 py-1 rounded truncate flex-1">{client.anon_key}</code>
                                                    <button onClick={() => safeCopy(client.anon_key)} className="text-slate-400 hover:text-indigo-600" title="Copy Key"><Copy size={14} /></button>
                                                </div>
                                            </div>
                                            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mt-2 pt-4 border-t border-slate-100">
                                                {/* Site URL - Editable on double click */}
                                                <div>
                                                    <label className="text-[9px] font-black text-slate-400 uppercase tracking-widest">Site URL (Fallback)</label>
                                                    {editingClientId === client.id ? (
                                                        <div className="mt-1 space-y-2">
                                                            <input 
                                                                type="text" 
                                                                value={editSiteUrl}
                                                                onChange={(e) => setEditSiteUrl(e.target.value)}
                                                                placeholder="https://app.example.com"
                                                                className="w-full bg-white border border-slate-200 rounded-xl py-2 px-3 text-[11px] font-mono outline-none focus:ring-2 focus:ring-indigo-500"
                                                            />
                                                        </div>
                                                    ) : (
                                                        <p 
                                                            onDoubleClick={() => startEditAppClient(client)}
                                                            className="text-[11px] font-mono font-bold text-slate-700 mt-1 truncate w-full cursor-pointer hover:bg-slate-50 rounded px-1 py-0.5 transition-colors"
                                                            title="Double-click to edit"
                                                        >
                                                            {client.site_url || 'Not set'}
                                                        </p>
                                                    )}
                                                </div>
                                                
                                                {/* Allowed Origins - Editable textarea */}
                                                <div>
                                                    <label className="text-[9px] font-black text-slate-400 uppercase tracking-widest">Allowed Origins (CORS)</label>
                                                    {editingClientId === client.id ? (
                                                        <div className="mt-1 space-y-2">
                                                            <textarea 
                                                                value={editAllowedOrigins}
                                                                onChange={(e) => setEditAllowedOrigins(e.target.value)}
                                                                placeholder="https://app.example.com, https://*.vercel.app"
                                                                rows={3}
                                                                className="w-full bg-white border border-slate-200 rounded-xl py-2 px-3 text-[10px] font-mono outline-none focus:ring-2 focus:ring-indigo-500 resize-none"
                                                            />
                                                            <p className="text-[9px] text-slate-400">Separate multiple origins with commas</p>
                                                        </div>
                                                    ) : (
                                                        <p 
                                                            onDoubleClick={() => startEditAppClient(client)}
                                                            className="text-[10px] font-medium text-slate-500 mt-1 cursor-pointer hover:bg-slate-50 rounded px-1 py-0.5 transition-colors"
                                                            title="Double-click to edit"
                                                        >
                                                            {client.allowed_origins?.join(', ') || 'Global Wide Open'}
                                                        </p>
                                                    )}
                                                </div>

                                                {/* Allowed Tables - Editable dropdown */}
                                                <div>
                                                    <div className="flex items-center justify-between">
                                                        <label className="text-[9px] font-black text-slate-400 uppercase tracking-widest">Allowed Tables</label>
                                                        <span className="text-[8px] text-slate-400">{(client.allowed_tables || []).length} selected</span>
                                                    </div>
                                                    {editingClientId === client.id ? (
                                                        <div className="mt-1 max-h-24 overflow-y-auto bg-white border border-slate-200 rounded-xl p-2 space-y-1">
                                                            {availableTables.length === 0 ? (
                                                                <p className="text-[9px] text-slate-400 text-center py-1">No tables</p>
                                                            ) : (
                                                                availableTables.map(table => (
                                                                    <label key={table} className="flex items-center gap-2 py-0.5 hover:bg-slate-50 rounded cursor-pointer">
                                                                        <input
                                                                            type="checkbox"
                                                                            checked={editAllowedTables.includes(table)}
                                                                            onChange={() => {
                                                                                const next = editAllowedTables.includes(table)
                                                                                    ? editAllowedTables.filter(t => t !== table)
                                                                                    : [...editAllowedTables, table];
                                                                                setEditAllowedTables(next);
                                                                            }}
                                                                            className="w-3 h-3 rounded border-slate-300 text-indigo-600"
                                                                        />
                                                                        <span className="text-[9px] font-mono text-slate-700">{table}</span>
                                                                    </label>
                                                                ))
                                                            )}
                                                        </div>
                                                    ) : (
                                                        <p 
                                                            onDoubleClick={() => startEditAppClient(client)}
                                                            className="text-[10px] font-medium text-slate-500 mt-1 cursor-pointer hover:bg-slate-50 rounded px-1 py-0.5 transition-colors truncate"
                                                            title="Double-click to edit"
                                                        >
                                                            {(client.allowed_tables || []).length > 0 
                                                                ? client.allowed_tables.join(', ') 
                                                                : 'All tables allowed'}
                                                        </p>
                                                    )}
                                                </div>

                                                {/* Blocked Tables - Editable dropdown */}
                                                <div>
                                                    <div className="flex items-center justify-between">
                                                        <label className="text-[9px] font-black text-rose-400 uppercase tracking-widest">Blocked Tables</label>
                                                        <span className="text-[8px] text-slate-400">{(client.blocked_tables || []).length} blocked</span>
                                                    </div>
                                                    {editingClientId === client.id ? (
                                                        <div className="mt-1 max-h-24 overflow-y-auto bg-white border border-slate-200 rounded-xl p-2 space-y-1">
                                                            {availableTables.length === 0 ? (
                                                                <p className="text-[9px] text-slate-400 text-center py-1">No tables</p>
                                                            ) : (
                                                                availableTables.map(table => (
                                                                    <label key={table} className="flex items-center gap-2 py-0.5 hover:bg-slate-50 rounded cursor-pointer">
                                                                        <input
                                                                            type="checkbox"
                                                                            checked={editBlockedTables.includes(table)}
                                                                            onChange={() => {
                                                                                const next = editBlockedTables.includes(table)
                                                                                    ? editBlockedTables.filter(t => t !== table)
                                                                                    : [...editBlockedTables, table];
                                                                                setEditBlockedTables(next);
                                                                            }}
                                                                            className="w-3 h-3 rounded border-slate-300 text-rose-600"
                                                                        />
                                                                        <span className="text-[9px] font-mono text-slate-700">{table}</span>
                                                                    </label>
                                                                ))
                                                            )}
                                                        </div>
                                                    ) : (
                                                        <p 
                                                            onDoubleClick={() => startEditAppClient(client)}
                                                            className="text-[10px] font-medium text-rose-500 mt-1 cursor-pointer hover:bg-slate-50 rounded px-1 py-0.5 transition-colors truncate"
                                                            title="Double-click to edit"
                                                        >
                                                            {(client.blocked_tables || []).length > 0 
                                                                ? client.blocked_tables.join(', ') 
                                                                : 'No blocked tables'}
                                                        </p>
                                                    )}
                                                </div>
                                            </div>
                                        </div>
                                    ))}
                                </div>
                            </div>

                            {/* GLOBAL IDENTITY POLICIES (REDESIGNED) */}
                        </div>
                    </div>
                )}

                {/* MESSAGING SECTION */}
                {activeSection === 'messaging' && (
                    <div className="p-10">
                        <div className="mb-8">
                            <h2 className="text-3xl font-black text-slate-900 tracking-tighter">Messaging & Templates</h2>
                            <p className="text-xs text-slate-400 font-bold mt-1">Configure delivery gateway, email templates, i18n template library, and identity policies.</p>
                        </div>
                        <div className="space-y-8">
                            <div className="bg-white border border-slate-200 rounded-[3rem] p-12 shadow-sm">
                                <div className="flex items-center justify-between mb-8">
                                    <div className="flex items-center gap-4">
                                        <div className="w-12 h-12 bg-indigo-50 text-indigo-600 rounded-2xl flex items-center justify-center"><MessageSquare size={20} /></div>
                                        <div>
                                            <h3 className="text-2xl font-black text-slate-900 tracking-tight">Messaging & Policies</h3>
                                            <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-1">Global Identity Flows & Templates</p>
                                        </div>
                                    </div>
                                </div>

                                {/* TABS */}
                                <div className="flex gap-2 p-1 bg-slate-100 rounded-2xl mb-8 w-fit">
                                    {['gateway', 'templates', 'library', 'policies'].map((t) => (
                                        <button
                                            key={t}
                                            onClick={() => setEmailTab(t as any)}
                                            className={`px-6 py-2.5 text-[10px] font-black uppercase tracking-widest rounded-xl transition-all ${emailTab === t ? 'bg-white shadow text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                                        >
                                            {t}
                                        </button>
                                    ))}
                                </div>

                                {/* TAB 1: GATEWAY (PROVIDER CONFIG) */}
                                {emailTab === 'gateway' && (
                                    <div className="space-y-8 animate-in fade-in slide-in-from-right-2">
                                        <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
                                            <div className="space-y-4">
                                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Delivery Channels (Multi-Select)</label>
                                                <div className="grid grid-cols-3 gap-3">
                                                    <button
                                                        onClick={() => setEmailGateway({
                                                            ...emailGateway,
                                                            delivery_methods: (emailGateway.delivery_methods || []).includes('smtp')
                                                                ? (emailGateway.delivery_methods || []).filter((m: string) => m !== 'smtp')
                                                                : [...(emailGateway.delivery_methods || []), 'smtp']
                                                        })}
                                                        className={`py-4 rounded-2xl border text-xs font-bold transition-all flex flex-col items-center gap-2 ${(emailGateway.delivery_methods || []).includes('smtp') ? 'bg-indigo-50 border-indigo-200 text-indigo-700 shadow-inner' : 'border-slate-200 text-slate-400'}`}>
                                                        <Server size={18} /> SMTP
                                                    </button>
                                                    <button
                                                        onClick={() => setEmailGateway({
                                                            ...emailGateway,
                                                            delivery_methods: (emailGateway.delivery_methods || []).includes('resend')
                                                                ? (emailGateway.delivery_methods || []).filter((m: string) => m !== 'resend')
                                                                : [...(emailGateway.delivery_methods || []), 'resend']
                                                        })}
                                                        className={`py-4 rounded-2xl border text-xs font-bold transition-all flex flex-col items-center gap-2 ${(emailGateway.delivery_methods || []).includes('resend') ? 'bg-indigo-50 border-indigo-200 text-indigo-700 shadow-inner' : 'border-slate-200 text-slate-400'}`}>
                                                        <Send size={18} /> Resend
                                                    </button>
                                                    <button
                                                        onClick={() => setEmailGateway({
                                                            ...emailGateway,
                                                            delivery_methods: (emailGateway.delivery_methods || []).includes('webhook')
                                                                ? (emailGateway.delivery_methods || []).filter((m: string) => m !== 'webhook')
                                                                : [...(emailGateway.delivery_methods || []), 'webhook']
                                                        })}
                                                        className={`py-4 rounded-2xl border text-xs font-bold transition-all flex flex-col items-center gap-2 ${(emailGateway.delivery_methods || []).includes('webhook') ? 'bg-indigo-50 border-indigo-200 text-indigo-700 shadow-inner' : 'border-slate-200 text-slate-400'}`}>
                                                        <Plug size={18} /> Webhook
                                                    </button>
                                                </div>
                                            </div>

                                            {(!(emailGateway.delivery_methods || []).includes('webhook') || (emailGateway.delivery_methods || []).length > 1) && (
                                                <div className="space-y-2">
                                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Sender Email (From)</label>
                                                    <input
                                                        value={emailGateway.from_email || ''}
                                                        onChange={(e) => setEmailGateway({ ...emailGateway, from_email: e.target.value })}
                                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                                        placeholder="noreply@myapp.com"
                                                    />
                                                </div>
                                            )}
                                        </div>

                                        {(emailGateway.delivery_methods || []).includes('resend') && (
                                            <div className="space-y-2 animate-in fade-in slide-in-from-top-1">
                                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Resend API Key</label>
                                                <input
                                                    type="password"
                                                    value={emailGateway.resend_api_key || ''}
                                                    onChange={(e) => setEmailGateway({ ...emailGateway, resend_api_key: e.target.value })}
                                                    className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none font-mono"
                                                    placeholder="re_123..."
                                                />
                                            </div>
                                        )}

                                        {(emailGateway.delivery_methods || []).includes('smtp') && (
                                            <div className="grid grid-cols-2 gap-4 animate-in fade-in slide-in-from-top-1">
                                                <div className="space-y-2">
                                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">SMTP Host</label>
                                                    <input
                                                        value={emailGateway.smtp_host || ''}
                                                        onChange={(e) => setEmailGateway({ ...emailGateway, smtp_host: e.target.value })}
                                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                                        placeholder="smtp.gmail.com"
                                                    />
                                                </div>
                                                <div className="space-y-2">
                                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Port</label>
                                                    <input
                                                        value={emailGateway.smtp_port || 587}
                                                        onChange={(e) => setEmailGateway({ ...emailGateway, smtp_port: parseInt(e.target.value) })}
                                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                                        type="number"
                                                    />
                                                </div>
                                                <div className="space-y-2">
                                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">User</label>
                                                    <input
                                                        value={emailGateway.smtp_user || ''}
                                                        onChange={(e) => setEmailGateway({ ...emailGateway, smtp_user: e.target.value })}
                                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                                    />
                                                </div>
                                                <div className="space-y-2">
                                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Password</label>
                                                    <input
                                                        type="password"
                                                        value={emailGateway.smtp_pass || ''}
                                                        onChange={(e) => setEmailGateway({ ...emailGateway, smtp_pass: e.target.value })}
                                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                                    />
                                                </div>
                                            </div>
                                        )}

                                        {(emailGateway.delivery_methods || []).includes('webhook') && (
                                            <div className="space-y-2 animate-in fade-in slide-in-from-top-1">
                                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Webhook URL</label>
                                                <input
                                                    value={emailGateway.webhook_url || ''}
                                                    onChange={(e) => setEmailGateway({ ...emailGateway, webhook_url: e.target.value })}
                                                    className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                                    placeholder="https://n8n.webhook/..."
                                                />
                                            </div>
                                        )}

                                        <div className="pt-4 border-t border-slate-100">
                                            <button onClick={handleSaveEmailCenter} disabled={executing} className="w-full bg-slate-900 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-600 transition-all flex items-center justify-center gap-2">
                                                {executing ? <Loader2 className="animate-spin" size={14} /> : 'Save Connection Settings'}
                                            </button>
                                        </div>
                                    </div>
                                )}

                                {/* TAB 2: TEMPLATES */}
                                {emailTab === 'templates' && (
                                    <div className="space-y-6 animate-in fade-in slide-in-from-right-2">
                                        <div className="flex gap-2 p-1 bg-slate-50 rounded-xl border border-slate-100 overflow-x-auto">
                                            {['confirmation', 'recovery', 'magic_link', 'login_alert', 'welcome_email'].map((t) => (
                                                <button
                                                    key={t}
                                                    onClick={() => setActiveTemplateTab(t as any)}
                                                    className={`flex-1 py-2 px-4 text-[10px] font-black uppercase tracking-widest rounded-lg transition-all whitespace-nowrap ${activeTemplateTab === t ? 'bg-white shadow text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
                                                >
                                                    {t.replace('_', ' ')}
                                                </button>
                                            ))}
                                        </div>

                                        <div className="space-y-4">
                                            <div className="space-y-2">
                                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Subject</label>
                                                <input
                                                    value={emailTemplates[activeTemplateTab]?.subject || ''}
                                                    onChange={(e) => setEmailTemplates({ ...emailTemplates, [activeTemplateTab]: { ...emailTemplates[activeTemplateTab], subject: e.target.value } })}
                                                    className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-6 text-sm font-bold text-slate-900 outline-none"
                                                />
                                            </div>
                                            <div className="space-y-2">
                                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">HTML Body</label>
                                                <textarea
                                                    value={emailTemplates[activeTemplateTab]?.body || ''}
                                                    onChange={(e) => setEmailTemplates({ ...emailTemplates, [activeTemplateTab]: { ...emailTemplates[activeTemplateTab], body: e.target.value } })}
                                                    className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-6 text-sm font-medium text-slate-900 outline-none min-h-[250px] font-mono"
                                                />
                                                <p className="text-[10px] text-slate-400 px-2">Variables: <code>{"{{ .ConfirmationURL }}"}</code>, <code>{"{{ .Token }}"}</code>, <code>{"{{ .Email }}"}</code>, <code>{"{{ .Date }}"}</code></p>
                                            </div>
                                        </div>

                                        <button onClick={handleSaveEmailCenter} disabled={executing} className="w-full bg-slate-900 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-600 transition-all flex items-center justify-center gap-2">
                                            {executing ? <Loader2 className="animate-spin" size={14} /> : 'Save Templates'}
                                        </button>
                                    </div>
                                )}

                                {/* TAB: TEMPLATE LIBRARY (i18n) */}
                                {emailTab === 'library' && (
                                    <div className="space-y-6 animate-in fade-in slide-in-from-right-2">
                                        <div className="flex justify-between items-center">
                                            <div>
                                                <h4 className="text-sm font-black text-slate-900">Message Template Library</h4>
                                                <p className="text-[10px] text-slate-400 font-bold mt-1">Reusable i18n templates for OTPs, Confirmations, Alerts, and more — across all strategies.</p>
                                            </div>
                                            <button onClick={() => setShowCreateTemplateModal(true)} className="bg-indigo-600 text-white px-5 py-2.5 rounded-2xl text-[10px] font-black uppercase tracking-widest flex items-center gap-2 hover:bg-indigo-700 transition-all shadow-lg">
                                                <Plus size={14} /> New Template
                                            </button>
                                        </div>

                                        {Object.keys(messagingTemplates).length === 0 && (
                                            <div className="py-16 text-center border-2 border-dashed border-slate-200 rounded-3xl">
                                                <LayoutTemplate size={40} className="mx-auto text-slate-300 mb-4" />
                                                <p className="text-sm font-bold text-slate-400">No templates yet</p>
                                                <p className="text-[10px] text-slate-400 mt-1">Create your first messaging template to enable i18n across all strategies.</p>
                                            </div>
                                        )}

                                        {/* Template Gallery Cards */}
                                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                            {Object.values(messagingTemplates).map((tpl: any) => (
                                                <div
                                                    key={tpl.id}
                                                    onClick={() => { setEditingTemplate(tpl.id); setEditingVariantLang(tpl.default_language); }}
                                                    className={`relative p-6 rounded-[2rem] border cursor-pointer transition-all group ${editingTemplate === tpl.id ? 'bg-indigo-50 border-indigo-300 shadow-lg' : 'bg-white border-slate-200 hover:shadow-md hover:border-slate-300'}`}
                                                >
                                                    <button
                                                        onClick={(e) => { e.stopPropagation(); handleDeleteTemplate(tpl.id); }}
                                                        className="absolute top-4 right-4 p-1.5 text-slate-300 hover:text-rose-500 opacity-0 group-hover:opacity-100 transition-all"
                                                    ><Trash2 size={14} /></button>
                                                    <div className="flex items-start gap-3 mb-3">
                                                        <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 ${editingTemplate === tpl.id ? 'bg-indigo-600 text-white' : 'bg-slate-100 text-slate-500'}`}>
                                                            <LayoutTemplate size={18} />
                                                        </div>
                                                        <div className="min-w-0">
                                                            <h5 className="font-bold text-slate-900 text-sm truncate">{tpl.name}</h5>
                                                            <p className="text-[9px] font-black text-indigo-600 uppercase tracking-widest mt-0.5">{tpl.type.replace(/_/g, ' ')}</p>
                                                        </div>
                                                    </div>
                                                    <div className="flex items-center gap-2 flex-wrap">
                                                        <span className="text-[9px] font-bold bg-slate-100 text-slate-500 px-2 py-0.5 rounded-lg">Default: {tpl.default_language}</span>
                                                        <span className="text-[9px] font-bold bg-slate-100 text-slate-500 px-2 py-0.5 rounded-lg">{Object.keys(tpl.variants || {}).length} variant{Object.keys(tpl.variants || {}).length !== 1 ? 's' : ''}</span>
                                                    </div>
                                                </div>
                                            ))}
                                        </div>

                                        {/* INLINE VARIANT EDITOR */}
                                        {editingTemplate && messagingTemplates[editingTemplate] && (() => {
                                            const tpl = messagingTemplates[editingTemplate];
                                            const variantKeys = Object.keys(tpl.variants || {});
                                            const currentVariant = tpl.variants?.[editingVariantLang] || { subject: '', body: '' };

                                            return (
                                                <div className="bg-white border border-slate-200 rounded-[2.5rem] p-8 shadow-sm space-y-6">
                                                    <div className="flex items-center justify-between">
                                                        <div>
                                                            <h4 className="text-lg font-black text-slate-900 flex items-center gap-2">
                                                                <Edit2 size={16} className="text-indigo-600" /> {tpl.name}
                                                            </h4>
                                                            <p className="text-[9px] font-black text-slate-400 uppercase tracking-widest mt-1">{tpl.type.replace(/_/g, ' ')} • Default: {tpl.default_language}</p>
                                                        </div>
                                                        <button onClick={() => setEditingTemplate(null)} className="p-2 text-slate-400 hover:text-slate-600 hover:bg-slate-50 rounded-xl"><X size={18} /></button>
                                                    </div>

                                                    {/* Language Variant Tabs */}
                                                    <div className="flex items-center gap-2 flex-wrap">
                                                        {variantKeys.map(lang => (
                                                            <button
                                                                key={lang}
                                                                onClick={() => setEditingVariantLang(lang)}
                                                                className={`px-4 py-2 rounded-xl text-[10px] font-black uppercase tracking-widest transition-all flex items-center gap-1.5 ${editingVariantLang === lang ? 'bg-indigo-600 text-white shadow-md' : 'bg-slate-100 text-slate-500 hover:bg-slate-200'}`}
                                                            >
                                                                <Globe size={12} /> {lang}
                                                                {lang === tpl.default_language && <span className="text-[8px] opacity-70">(default)</span>}
                                                            </button>
                                                        ))}
                                                        <div className="flex items-center gap-1 ml-2">
                                                            <input
                                                                placeholder="es-ES"
                                                                className="w-20 bg-slate-50 border border-slate-200 rounded-lg px-2 py-1.5 text-[10px] font-bold outline-none"
                                                                onKeyDown={(e) => {
                                                                    if (e.key === 'Enter') {
                                                                        handleAddVariant(editingTemplate!, (e.target as HTMLInputElement).value.trim());
                                                                        (e.target as HTMLInputElement).value = '';
                                                                    }
                                                                }}
                                                            />
                                                            <span className="text-[9px] text-slate-400 font-bold">Enter to add</span>
                                                        </div>
                                                    </div>

                                                    {/* Subject + Body Editor */}
                                                    <div className="space-y-4">
                                                        <div className="space-y-2">
                                                            <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Subject ({editingVariantLang})</label>
                                                            <input
                                                                value={currentVariant.subject}
                                                                onChange={(e) => handleUpdateVariant(editingTemplate!, editingVariantLang, 'subject', e.target.value)}
                                                                className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-6 text-sm font-bold text-slate-900 outline-none"
                                                                placeholder="e.g. Your Verification Code"
                                                            />
                                                        </div>
                                                        <div className="space-y-2">
                                                            <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Body ({editingVariantLang})</label>
                                                            <textarea
                                                                value={currentVariant.body}
                                                                onChange={(e) => handleUpdateVariant(editingTemplate!, editingVariantLang, 'body', e.target.value)}
                                                                className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-6 text-sm font-medium text-slate-900 outline-none min-h-[180px] font-mono"
                                                                placeholder="HTML or plain text body..."
                                                            />
                                                            <p className="text-[10px] text-slate-400 px-2">Variables: <code>{"{{ .Code }}"}</code>, <code>{"{{ .ConfirmationURL }}"}</code>, <code>{"{{ .Email }}"}</code>, <code>{"{{ .AppName }}"}</code>, <code>{"{{ .Expiration }}"}</code>, <code>{"{{ .Date }}"}</code>, <code>{"{{ .Identifier }}"}</code>, <code>{"{{ .Strategy }}"}</code></p>
                                                        </div>
                                                    </div>

                                                    {/* Remove Variant */}
                                                    {variantKeys.length > 1 && editingVariantLang !== tpl.default_language && (
                                                        <button
                                                            onClick={() => handleRemoveVariant(editingTemplate!, editingVariantLang)}
                                                            className="text-[10px] font-bold text-rose-500 hover:text-rose-700 flex items-center gap-1"
                                                        >
                                                            <Trash2 size={12} /> Remove &quot;{editingVariantLang}&quot; variant
                                                        </button>
                                                    )}
                                                </div>
                                            );
                                        })()}

                                        <button onClick={handleSaveTemplateLibrary} disabled={executing} className="w-full bg-slate-900 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-600 transition-all flex items-center justify-center gap-2">
                                            {executing ? <Loader2 className="animate-spin" size={14} /> : 'Save Template Library'}
                                        </button>
                                    </div>
                                )}

                                {/* TAB 3: POLICIES (FLOWS) */}
                                {emailTab === 'policies' && (
                                    <div className="space-y-8 animate-in fade-in slide-in-from-right-2">
                                        <div className="grid grid-cols-1 gap-4">
                                            <div className={`p-6 rounded-[2rem] border transition-all cursor-pointer ${emailPolicies.email_confirmation ? 'bg-indigo-50 border-indigo-200' : 'bg-slate-50 border-slate-200'}`} onClick={() => setEmailPolicies(p => ({ ...p, email_confirmation: !p.email_confirmation }))}>
                                                <div className="flex justify-between items-center">
                                                    <div>
                                                        <h4 className={`font-bold text-sm ${emailPolicies.email_confirmation ? 'text-indigo-900' : 'text-slate-500'}`}>Require Identity Confirmation</h4>
                                                        <p className="text-[10px] text-slate-400 mt-1">Users cannot login until they verify their primary identifier (Email, Phone, etc).</p>
                                                    </div>
                                                    <div className={`w-12 h-7 rounded-full p-1 transition-colors ${emailPolicies.email_confirmation ? 'bg-indigo-600' : 'bg-slate-300'}`}>
                                                        <div className={`w-5 h-5 bg-white rounded-full shadow-md transition-transform ${emailPolicies.email_confirmation ? 'translate-x-5' : ''}`}></div>
                                                    </div>
                                                </div>
                                            </div>

                                            <div className={`p-6 rounded-[2rem] border transition-all cursor-pointer ${emailPolicies.disable_magic_link ? 'bg-rose-50 border-rose-200' : 'bg-slate-50 border-slate-200'}`} onClick={() => setEmailPolicies(p => ({ ...p, disable_magic_link: !p.disable_magic_link }))}>
                                                <div className="flex justify-between items-center">
                                                    <div>
                                                        <h4 className={`font-bold text-sm ${emailPolicies.disable_magic_link ? 'text-rose-900' : 'text-slate-500'}`}>Disable Magic Links / Passwordless</h4>
                                                        <p className="text-[10px] text-slate-400 mt-1">Prevent users from logging in via OTP or link without a password.</p>
                                                    </div>
                                                    <div className={`w-12 h-7 rounded-full p-1 transition-colors ${emailPolicies.disable_magic_link ? 'bg-rose-600' : 'bg-slate-300'}`}>
                                                        <div className={`w-5 h-5 bg-white rounded-full shadow-md transition-transform ${emailPolicies.disable_magic_link ? 'translate-x-5' : ''}`}></div>
                                                    </div>
                                                </div>
                                            </div>

                                            <div className={`p-6 rounded-[2rem] border transition-all cursor-pointer ${emailPolicies.send_welcome_email ? 'bg-emerald-50 border-emerald-200' : 'bg-slate-50 border-slate-200'}`} onClick={() => setEmailPolicies(p => ({ ...p, send_welcome_email: !p.send_welcome_email }))}>
                                                <div className="flex justify-between items-center">
                                                    <div>
                                                        <h4 className={`font-bold text-sm ${emailPolicies.send_welcome_email ? 'text-emerald-900' : 'text-slate-500'}`}><PartyPopper className="inline mr-2" size={14} /> Send Welcome Message</h4>
                                                        <p className="text-[10px] text-slate-400 mt-1">Automatically trigger a welcome notification upon signup (or verification).</p>
                                                    </div>
                                                    <div className={`w-12 h-7 rounded-full p-1 transition-colors ${emailPolicies.send_welcome_email ? 'bg-emerald-500' : 'bg-slate-300'}`}>
                                                        <div className={`w-5 h-5 bg-white rounded-full shadow-md transition-transform ${emailPolicies.send_welcome_email ? 'translate-x-5' : ''}`}></div>
                                                    </div>
                                                </div>
                                            </div>

                                            <div className={`p-6 rounded-[2rem] border transition-all cursor-pointer ${emailPolicies.send_login_alert ? 'bg-amber-50 border-amber-200' : 'bg-slate-50 border-slate-200'}`} onClick={() => setEmailPolicies(p => ({ ...p, send_login_alert: !p.send_login_alert }))}>
                                                <div className="flex justify-between items-center">
                                                    <div>
                                                        <h4 className={`font-bold text-sm ${emailPolicies.send_login_alert ? 'text-amber-900' : 'text-slate-500'}`}><BellRing className="inline mr-2" size={14} /> Login Notification Alert</h4>
                                                        <p className="text-[10px] text-slate-400 mt-1">Notify user every time a successful login occurs across all providers.</p>
                                                    </div>
                                                    <div className={`w-12 h-7 rounded-full p-1 transition-colors ${emailPolicies.send_login_alert ? 'bg-amber-500' : 'bg-slate-300'}`}>
                                                        <div className={`w-5 h-5 bg-white rounded-full shadow-md transition-transform ${emailPolicies.send_login_alert ? 'translate-x-5' : ''}`}></div>
                                                    </div>
                                                </div>
                                            </div>

                                            <div className={`p-6 rounded-[2rem] border transition-all cursor-pointer ${emailPolicies.notify_new_biometric_device ? 'bg-indigo-50 border-indigo-200' : 'bg-slate-50 border-slate-200'}`} onClick={() => setEmailPolicies(p => ({ ...p, notify_new_biometric_device: !p.notify_new_biometric_device }))}>
                                                <div className="flex justify-between items-center">
                                                    <div>
                                                        <h4 className={`font-bold text-sm ${emailPolicies.notify_new_biometric_device ? 'text-indigo-900' : 'text-slate-500'}`}><Fingerprint className="inline mr-2" size={14} /> Biometric Key Alerts (Nexus)</h4>
                                                        <p className="text-[10px] text-slate-400 mt-1">Trigger a Nexus Automation workflow/alert when a new biometric key or device is successfully registered.</p>
                                                    </div>
                                                    <div className={`w-12 h-7 rounded-full p-1 transition-colors ${emailPolicies.notify_new_biometric_device ? 'bg-indigo-600' : 'bg-slate-300'}`}>
                                                        <div className={`w-5 h-5 bg-white rounded-full shadow-md transition-transform ${emailPolicies.notify_new_biometric_device ? 'translate-x-5' : ''}`}></div>
                                                    </div>
                                                </div>
                                            </div>
                                        </div>

                                        <div className="pt-4 border-t border-slate-100">
                                            <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1 mb-2 block">Login Webhook URL (Optional)</label>
                                            <input
                                                value={emailPolicies.login_webhook_url || ''}
                                                onChange={(e) => setEmailPolicies(p => ({ ...p, login_webhook_url: e.target.value }))}
                                                className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                                placeholder="https://api.myapp.com/webhooks/login"
                                            />
                                            <p className="text-[10px] text-slate-400 mt-2 px-1">If set, a POST request will be sent here every time a user successfully logs in.</p>
                                        </div>
                                    </div>
                                )}
                            </div>
                        </div>
                    </div>
                )}

                {/* SCHEMA SECTION */}
                {activeSection === 'schema' && (
                    <div className="p-10">
                        <div className="mb-8">
                            <h2 className="text-3xl font-black text-slate-900 tracking-tighter">Schema & Data Linking</h2>
                            <p className="text-xs text-slate-400 font-bold mt-1">Link application tables with the auth users table for automatic foreign key relationships.</p>
                        </div>
                        <div className="space-y-8">
                            <div className="bg-white border border-slate-200 rounded-[3rem] p-12 shadow-sm">
                                <div className="flex items-center gap-4 mb-8">
                                    <div className="w-12 h-12 bg-indigo-50 text-indigo-600 rounded-2xl flex items-center justify-center"><Layers size={20} /></div>
                                    <div className="flex-1">
                                        <h3 className="text-2xl font-black text-slate-900 tracking-tight">Schema Concatenation</h3>
                                        <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-1">Multi-Table Linking & Foreign Keys</p>
                                    </div>
                                    {/* Schema Selector */}
                                    <div className="flex items-center gap-2">
                                        <span className="text-[10px] font-bold text-slate-400 uppercase tracking-widest">Schema:</span>
                                        <select
                                            value={selectedSchemaForLinking}
                                            onChange={(e) => setSelectedSchemaForLinking(e.target.value)}
                                            className="bg-white border border-slate-200 rounded-xl py-2 px-3 text-xs font-bold text-slate-700 outline-none focus:ring-2 focus:ring-indigo-500/20"
                                        >
                                            {availableSchemas.map(schema => (
                                                <option key={schema} value={schema}>{schema}</option>
                                            ))}
                                        </select>
                                    </div>
                                </div>
                                <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-4">
                                    {(tablesBySchema[selectedSchemaForLinking] || []).map(table => {
                                        const isLinked = linkedTables.includes(table);
                                        return (
                                            <button
                                                key={table}
                                                onClick={() => toggleLinkedTable(table)}
                                                disabled={executing}
                                                className={`p-4 rounded-2xl border flex flex-col items-center gap-3 transition-all ${isLinked ? 'bg-indigo-600 border-indigo-600 text-white shadow-lg' : 'bg-slate-50 border-slate-200 text-slate-500 hover:bg-white hover:shadow-md'}`}
                                            >
                                                <div className={`w-10 h-10 rounded-xl flex items-center justify-center ${isLinked ? 'bg-white/20' : 'bg-white'}`}>
                                                    {isLinked ? <Link size={18} /> : <Unlink size={18} />}
                                                </div>
                                                <span className="text-xs font-black truncate max-w-full px-2">{table}</span>
                                            </button>
                                        );
                                    })}
                                    {(tablesBySchema[selectedSchemaForLinking] || []).length === 0 && <p className="col-span-full text-center text-slate-400 text-xs font-medium py-8">Nenhuma tabela disponível no schema {selectedSchemaForLinking}.</p>}
                                </div>
                            </div>
                        </div>
                    </div>
                )}

                {/* STRATEGIES SECTION */}
                {activeSection === 'strategies' && (
                    <div className="p-10">
                        <div className="mb-8">
                            <h2 className="text-3xl font-black text-slate-900 tracking-tighter">Identity Strategies</h2>
                            <p className="text-xs text-slate-400 font-bold mt-1">Configure authentication providers — from OAuth social login to custom identity strategies.</p>
                        </div>
                        <div className="space-y-8">

                            {/* BASE CLIENT - Default Key Configuration */}
                            <div className="bg-white border border-slate-200 rounded-[3rem] p-12 shadow-sm">
                                <div className="flex items-center gap-4 mb-8">
                                    <div className="w-12 h-12 bg-indigo-50 text-indigo-600 rounded-2xl flex items-center justify-center"><Lock size={20} /></div>
                                    <div>
                                        <h3 className="text-2xl font-black text-slate-900 tracking-tight">Default Client (Base Key)</h3>
                                        <p className="text-xs text-slate-400 font-bold mt-1">Primary fallback anon_key for older apps and API Docs.</p>
                                    </div>
                                </div>
                                <div className="p-6 rounded-[2rem] border border-slate-200 bg-slate-50 flex flex-col gap-4">
                                    <div>
                                        <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1 mb-2 block">Global Redirect oauth/auth Site URL</label>
                                        <div className="flex gap-2">
                                            <input value={siteUrl} onChange={(e) => setSiteUrl(e.target.value)} placeholder="https://app.cascata.io" className="flex-1 bg-white border border-slate-200 rounded-xl py-2 px-4 text-xs font-bold font-mono outline-none" />
                                            <button onClick={handleSaveSiteUrl} disabled={executing} className="bg-indigo-600 text-white px-4 rounded-xl font-bold text-[10px] uppercase tracking-widest hover:bg-indigo-700 transition-all">Save</button>
                                        </div>
                                    </div>
                                </div>
                            </div>

                            {/* Social Providers */}
                            <div className="bg-white border border-slate-200 rounded-[3rem] p-12 shadow-sm">
                                <div className="flex items-center gap-4 mb-8">
                                    <div className="w-12 h-12 bg-rose-50 text-rose-600 rounded-2xl flex items-center justify-center"><Globe size={20} /></div>
                                    <h3 className="text-2xl font-black text-slate-900 tracking-tight">Social & Enterprise Providers</h3>
                                </div>
                                <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                                    {/* Google */}
                                    <button onClick={() => openProviderConfig('google')} className="flex flex-col items-center gap-4 p-8 border-2 border-indigo-50 bg-indigo-50/20 rounded-[2.5rem] hover:border-indigo-200 transition-all group">
                                        <div className="w-16 h-16 bg-white rounded-full flex items-center justify-center shadow-lg text-rose-600"><Globe size={32} /></div>
                                        <div className="text-center">
                                            <h4 className="font-black text-slate-900">Google Workspace</h4>
                                            <span className="text-[10px] font-black text-emerald-600 uppercase tracking-widest bg-emerald-50 px-2 py-1 rounded-lg mt-2 inline-block">Configurar</span>
                                        </div>
                                    </button>
                                    {/* GitHub */}
                                    <button onClick={() => openProviderConfig('github')} className="flex flex-col items-center gap-4 p-8 border-2 border-slate-100 bg-slate-50/50 rounded-[2.5rem] hover:border-slate-300 transition-all group">
                                        <div className="w-16 h-16 bg-white rounded-full flex items-center justify-center shadow-lg text-slate-900"><Github size={32} /></div>
                                        <div className="text-center">
                                            <h4 className="font-black text-slate-900">GitHub</h4>
                                            <span className="text-[10px] font-black text-emerald-600 uppercase tracking-widest bg-emerald-50 px-2 py-1 rounded-lg mt-2 inline-block">Configurar</span>
                                        </div>
                                    </button>
                                </div>
                            </div>
                            {/* Strategy Cards (Custom & System) */}
                            <div className="space-y-4">
                                <div className="flex items-center justify-between px-4">
                                    <h3 className="text-[11px] font-black text-slate-400 uppercase tracking-[0.4em]">Active Strategies</h3>
                                    <button onClick={() => setShowNewStrategy(true)} className="text-[10px] font-black text-indigo-600 uppercase tracking-widest hover:bg-indigo-50 px-4 py-2 rounded-xl transition-all flex items-center gap-2"><Plus size={12} /> Criar Estratégia Customizada</button>
                                </div>

                                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-8">
                                    {Object.keys(strategies)
                                        .filter(stKey => !['google', 'github'].includes(stKey)) // Filter out social providers from this list
                                        .map(stKey => {
                                            const config = strategies[stKey];
                                            const isDefault = ['email'].includes(stKey);

                                            return (
                                                <div key={stKey} className={`relative bg-white border rounded-[2.5rem] p-8 shadow-sm transition-all group ${config.enabled ? 'border-indigo-200' : 'border-slate-200 opacity-70'}`}>
                                                    <div className="flex justify-between items-start mb-6">
                                                        <div className={`w-14 h-14 rounded-2xl flex items-center justify-center text-white shadow-lg ${config.enabled ? 'bg-indigo-600' : 'bg-slate-300'}`}>
                                                            {stKey === 'email' && <Mail size={24} />}
                                                            {stKey === 'cpf' && <CreditCard size={24} />}
                                                            {stKey === 'phone' && <Smartphone size={24} />}
                                                            {!isDefault && <Hash size={24} />}
                                                        </div>
                                                        <button onClick={() => toggleStrategy(stKey)} className={`w-12 h-7 rounded-full p-1 transition-colors ${config.enabled ? 'bg-emerald-500' : 'bg-slate-200'}`}>
                                                            <div className={`w-5 h-5 bg-white rounded-full shadow-sm transition-transform ${config.enabled ? 'translate-x-5' : ''}`}></div>
                                                        </button>
                                                    </div>

                                                    <div className="mb-6">
                                                        <div className="flex items-center justify-between">
                                                            <h4 className="text-xl font-black text-slate-900 capitalize truncate" title={stKey}>{stKey}</h4>
                                                            {!isDefault && (
                                                                <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                                                                    <button onClick={() => handleDeleteStrategy(stKey)} className="p-1 text-slate-300 hover:text-rose-600"><Trash2 size={12} /></button>
                                                                </div>
                                                            )}
                                                        </div>
                                                        <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-2">
                                                            {config.rules?.length || 0} Origin Rules • {config.jwt_expiration || '24h'}
                                                        </p>
                                                        <div className="flex flex-wrap gap-1.5 mt-3">
                                                            {config.password_enabled !== false && <span className="bg-slate-100 text-slate-500 text-[9px] font-black uppercase tracking-widest px-2 py-0.5 rounded-md">Password</span>}
                                                            {config.magiclink_enabled !== false && <span className="bg-purple-50 text-purple-600 text-[9px] font-black uppercase tracking-widest px-2 py-0.5 rounded-md">Magic Link</span>}
                                                            {config.otp_enabled !== false && <span className="bg-indigo-50 text-indigo-600 text-[9px] font-black uppercase tracking-widest px-2 py-0.5 rounded-md">OTP</span>}
                                                            {config.biometria_enabled && <span className="bg-emerald-50 text-emerald-600 text-[9px] font-black uppercase tracking-widest px-2 py-0.5 rounded-md">Passkey</span>}
                                                            {config.totp_enabled && <span className="bg-amber-50 text-amber-600 text-[9px] font-black uppercase tracking-widest px-2 py-0.5 rounded-md">TOTP MFA</span>}
                                                            {config.challenge_provider && <span className="bg-rose-50 text-rose-600 text-[9px] font-black uppercase tracking-widest px-2 py-0.5 rounded-md">Step-Up Provider</span>}
                                                        </div>
                                                    </div>

                                                    <button
                                                        onClick={() => {
                                                            setSelectedStrategy(stKey);
                                                            setStrategyConfig({ ...config });
                                                            setEditingStrategyName(stKey);
                                                            setShowConfigModal(true);
                                                        }}
                                                        className="w-full py-4 border border-slate-200 rounded-2xl text-[10px] font-black uppercase tracking-widest hover:bg-slate-50 transition-all flex items-center justify-center gap-2"
                                                    >
                                                        <Settings size={14} /> Advanced Config
                                                    </button>
                                                </div>
                                            );
                                        })}
                                </div>
                            </div>
                        </div>
                    </div>
                )}

                {/* SECURITY SECTION */}
                {activeSection === 'security' && (
                    <div className="p-10">
                        <div className="mb-8">
                            <h2 className="text-3xl font-black text-slate-900 tracking-tighter">Security & Protection</h2>
                            <p className="text-xs text-slate-400 font-bold mt-1">Brute force protection, identity confirmation policies, and session security.</p>
                        </div>
                        <div className="space-y-8">

                            {/* SECURITY & PROTECTION (Edge Firewall) */}
                            <div className="bg-white border border-slate-200 rounded-[3rem] p-12 shadow-sm">
                                <div className="flex items-center gap-4 mb-8">
                                    <div className="w-12 h-12 bg-rose-50 text-rose-600 rounded-2xl flex items-center justify-center"><ShieldAlert size={20} /></div>
                                    <div>
                                        <h3 className="text-2xl font-black text-slate-900 tracking-tight">Smart Lockout (Edge Firewall)</h3>
                                        <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-1">Identity-Agnostic Brute Force Protection</p>
                                    </div>
                                </div>

                                <div className="grid grid-cols-1 md:grid-cols-2 gap-8 mb-8">
                                    <div className="space-y-2">
                                        <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Max Attempts (Threshold)</label>
                                        <div className="flex items-center bg-slate-50 border border-slate-200 rounded-2xl px-4">
                                            <Target size={16} className="text-rose-400" />
                                            <input
                                                type="number"
                                                min="1"
                                                value={securityConfig.max_attempts}
                                                onChange={(e) => setSecurityConfig({ ...securityConfig, max_attempts: parseInt(e.target.value) })}
                                                className="w-full bg-transparent border-none py-3 px-4 text-sm font-bold text-slate-900 outline-none"
                                            />
                                        </div>
                                    </div>
                                    <div className="space-y-2">
                                        <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Lockout Duration (Minutes)</label>
                                        <div className="flex items-center bg-slate-50 border border-slate-200 rounded-2xl px-4">
                                            <Clock size={16} className="text-indigo-400" />
                                            <input
                                                type="number"
                                                min="1"
                                                value={securityConfig.lockout_minutes}
                                                onChange={(e) => setSecurityConfig({ ...securityConfig, lockout_minutes: parseInt(e.target.value) })}
                                                className="w-full bg-transparent border-none py-3 px-4 text-sm font-bold text-slate-900 outline-none"
                                            />
                                        </div>
                                    </div>
                                </div>

                                <div className="space-y-3 mb-8">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Protection Strategy</label>
                                    <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                                        <button
                                            onClick={() => setSecurityConfig({ ...securityConfig, strategy: 'hybrid' })}
                                            className={`p-4 rounded-2xl border text-left transition-all ${securityConfig.strategy === 'hybrid' ? 'bg-indigo-600 border-indigo-600 text-white shadow-lg' : 'bg-white border-slate-200 text-slate-500 hover:border-indigo-300'}`}
                                        >
                                            <span className="text-xs font-black uppercase block mb-1">Hybrid (IP + Identifier)</span>
                                            <span className={`text-[10px] ${securityConfig.strategy === 'hybrid' ? 'text-indigo-200' : 'text-slate-400'}`}>Locks IP + Identifier pair. Safest for shared networks.</span>
                                        </button>
                                        <button
                                            onClick={() => setSecurityConfig({ ...securityConfig, strategy: 'ip' })}
                                            className={`p-4 rounded-2xl border text-left transition-all ${securityConfig.strategy === 'ip' ? 'bg-indigo-600 border-indigo-600 text-white shadow-lg' : 'bg-white border-slate-200 text-slate-500 hover:border-indigo-300'}`}
                                        >
                                            <span className="text-xs font-black uppercase block mb-1">Strict IP</span>
                                            <span className={`text-[10px] ${securityConfig.strategy === 'ip' ? 'text-indigo-200' : 'text-slate-400'}`}>Locks IP address entirely. Good vs distributed Bots.</span>
                                        </button>
                                        <button
                                            onClick={() => setSecurityConfig({ ...securityConfig, strategy: 'identifier' })}
                                            className={`p-4 rounded-2xl border text-left transition-all ${securityConfig.strategy === 'identifier' || securityConfig.strategy === 'email' ? 'bg-indigo-600 border-indigo-600 text-white shadow-lg' : 'bg-white border-slate-200 text-slate-500 hover:border-indigo-300'}`}
                                        >
                                            <span className="text-xs font-black uppercase block mb-1">Strict Identifier</span>
                                            <span className={`text-[10px] ${securityConfig.strategy === 'identifier' || securityConfig.strategy === 'email' ? 'text-indigo-200' : 'text-slate-400'}`}>Protects specific account/phone/email only.</span>
                                        </button>
                                    </div>
                                </div>

                                {isAggressiveSecurity && (
                                    <div className="p-4 bg-amber-50 border border-amber-200 rounded-2xl flex items-start gap-3 mb-6 animate-in fade-in slide-in-from-top-2">
                                        <AlertCircle className="text-amber-600 shrink-0 mt-0.5" size={18} />
                                        <div>
                                            <h4 className="text-xs font-black text-amber-700 uppercase">Warning: Aggressive Thresholds</h4>
                                            <p className="text-[10px] text-amber-600 mt-1 leading-relaxed">
                                                A low max attempt or high duration might lock out legitimate users or administrators. Ensure recovery flows are functional.
                                            </p>
                                        </div>
                                    </div>
                                )}

                                <button onClick={handleSaveSecurity} disabled={executing} className="w-full bg-slate-900 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-600 transition-all flex items-center justify-center gap-2">
                                    {executing ? <Loader2 className="animate-spin" size={14} /> : 'Apply Security Policies'}
                                </button>
                            </div>
                        </div>
                    </div>
                )}
            </div>

            {/* PANIC MODAL */}
            {showPanicModal && (
                <div className="fixed inset-0 bg-rose-950/90 backdrop-blur-xl z-[1000] flex items-center justify-center p-8 animate-in fade-in zoom-in-95">
                    <div className="bg-white rounded-[4rem] max-w-lg w-full p-12 shadow-2xl border-4 border-rose-500 animate-pulse">
                        <div className="text-center mb-8">
                            <div className="w-24 h-24 bg-rose-100 text-rose-600 rounded-full flex items-center justify-center mx-auto mb-6 shadow-xl animate-bounce">
                                <ShieldAlert size={48} />
                            </div>
                            <h3 className="text-3xl font-black text-slate-900 tracking-tighter">Emergency Panic Revocation</h3>
                            <p className="text-xs text-slate-500 font-bold uppercase tracking-widest mt-2">Invalidate target sessions immediately</p>
                        </div>

                        <div className="space-y-6 mb-8">
                            <div className="space-y-2">
                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Neutralization Target</label>
                                <div className="grid grid-cols-2 gap-4">
                                    <button
                                        onClick={() => setPanicForm({ ...panicForm, target_type: 'user' })}
                                        className={`py-4 rounded-3xl border-2 font-black text-[10px] uppercase tracking-widest transition-all ${panicForm.target_type === 'user' ? 'bg-slate-900 border-slate-900 text-white shadow-xl' : 'bg-slate-50 border-slate-100 text-slate-400 hover:border-slate-300'}`}
                                    >
                                        Specific User
                                    </button>
                                    <button
                                        onClick={() => setPanicForm({ ...panicForm, target_type: 'global' })}
                                        className={`py-4 rounded-3xl border-2 font-black text-[10px] uppercase tracking-widest transition-all ${panicForm.target_type === 'global' ? 'bg-rose-600 border-rose-600 text-white shadow-xl' : 'bg-rose-50 border-rose-100 text-rose-400 hover:border-rose-300'}`}
                                    >
                                        Global Revocation
                                    </button>
                                </div>
                            </div>

                            {panicForm.target_type === 'user' && (
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">User Identifier (Email/UUID)</label>
                                    <input
                                        value={panicForm.target_value}
                                        onChange={(e) => setPanicForm({ ...panicForm, target_value: e.target.value })}
                                        className="w-full bg-slate-50 border-2 border-slate-100 rounded-[2rem] py-4 px-6 text-sm font-bold text-slate-900 outline-none focus:border-rose-300 transition-all font-mono"
                                        placeholder="user@target.com"
                                    />
                                </div>
                            )}

                            <div className="space-y-2">
                                <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Panic Reason (Audit Log)</label>
                                <textarea
                                    value={panicForm.reason}
                                    onChange={(e) => setPanicForm({ ...panicForm, reason: e.target.value })}
                                    className="w-full bg-slate-50 border-2 border-slate-100 rounded-[2rem] py-4 px-6 text-sm font-bold text-slate-900 outline-none focus:border-rose-300 transition-all h-24 resize-none"
                                />
                            </div>
                        </div>

                        <div className="flex gap-4">
                            <button onClick={() => setShowPanicModal(false)} className="flex-1 py-4 text-slate-400 font-bold text-[10px] uppercase tracking-widest hover:bg-slate-50 rounded-2xl transition-all">Abort Mission</button>
                            <button
                                onClick={handlePanic}
                                disabled={executing || (panicForm.target_type === 'user' && !panicForm.target_value)}
                                className="flex-1 py-4 bg-rose-600 text-white rounded-2xl font-black text-[10px] uppercase tracking-widest shadow-xl shadow-rose-200 hover:bg-rose-700 transition-all active:scale-95 disabled:opacity-50"
                            >
                                {executing ? <Loader2 className="animate-spin mx-auto" /> : 'Execute Neutralization'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* POLICY MODAL */}
            {showPolicyModal && (
                <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[800] flex items-center justify-center p-8 animate-in zoom-in-95">
                    <div className="bg-white rounded-[3.5rem] max-w-3xl w-full p-12 shadow-2xl flex flex-col max-h-[90vh]">
                        <div className="flex justify-between items-center mb-10">
                            <div>
                                <h3 className="text-3xl font-black text-slate-900 tracking-tighter">{editingPolicy ? 'Update Sovereign Law' : 'Declare New Policy'}</h3>
                                <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-1">Sovereign Orchestration Protocol</p>
                            </div>
                            <button onClick={() => setShowPolicyModal(false)} className="p-4 bg-slate-50 rounded-full hover:bg-slate-100 transition-all"><X size={24} /></button>
                        </div>

                        <div className="flex-1 overflow-y-auto space-y-10 pr-4">
                            <div className="grid grid-cols-2 gap-8">
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Policy Internal Name</label>
                                    <input value={policyForm.name} onChange={e => setPolicyForm({ ...policyForm, name: e.target.value })} className="w-full bg-slate-50 border-none rounded-2xl py-4 px-6 text-sm font-bold outline-none ring-1 ring-slate-100" />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Execution Priority</label>
                                    <input type="number" value={policyForm.priority} onChange={e => setPolicyForm({ ...policyForm, priority: parseInt(e.target.value) })} className="w-full bg-slate-50 border-none rounded-2xl py-4 px-6 text-sm font-bold outline-none ring-1 ring-slate-100" />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Target Identity Type (Provider)</label>
                                    <select value={policyForm.provider} onChange={e => setPolicyForm({ ...policyForm, provider: e.target.value })} className="w-full bg-slate-50 border-none rounded-2xl py-4 px-6 text-sm font-bold outline-none ring-1 ring-slate-100">
                                        <option value="*">All Providers (*)</option>
                                        <option value="email">Email</option>
                                        <option value="phone">Phone</option>
                                        <option value="google">Google</option>
                                        <option value="github">GitHub</option>
                                        {Object.keys(strategies).filter(k => !['email', 'phone', 'google', 'github'].includes(k)).map(k => <option key={k} value={k}>{k}</option>)}
                                    </select>
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Origin Enforcement (Domain)</label>
                                    <input value={policyForm.origin} onChange={e => setPolicyForm({ ...policyForm, origin: e.target.value })} placeholder="e.g. app.com or *" className="w-full bg-slate-50 border-none rounded-2xl py-4 px-6 text-sm font-bold outline-none ring-1 ring-slate-100 font-mono" />
                                </div>
                            </div>

                            <div className="bg-indigo-50/50 rounded-[2.5rem] p-10 space-y-8 border border-indigo-100">
                                <h4 className="text-xs font-black text-indigo-900 uppercase tracking-[0.3em]">Security Enforcement Layers</h4>
                                <div className="grid grid-cols-2 gap-6">
                                    <div onClick={() => setPolicyForm({ ...policyForm, require_password: !policyForm.require_password })} className={`p-6 rounded-3xl border-2 flex items-center justify-between cursor-pointer transition-all ${policyForm.require_password ? 'bg-white border-indigo-500 shadow-lg' : 'bg-slate-50/50 border-slate-100 opacity-60'}`}>
                                        <div>
                                            <span className="text-xs font-black text-slate-900 block">Require Password</span>
                                            <span className="text-[9px] text-slate-400 font-bold uppercase">Layer 1 Validation</span>
                                        </div>
                                        {policyForm.require_password ? <CheckCircle2 className="text-indigo-600" /> : <Square className="text-slate-300" />}
                                    </div>

                                    <div onClick={() => setPolicyForm({ ...policyForm, require_otp: !policyForm.require_otp })} className={`p-6 rounded-3xl border-2 flex items-center justify-between cursor-pointer transition-all ${policyForm.require_otp ? 'bg-white border-rose-500 shadow-lg' : 'bg-slate-50/50 border-slate-100 opacity-60'}`}>
                                        <div>
                                            <span className="text-xs font-black text-slate-900 block">Enforce OTP/MFA</span>
                                            <span className="text-[9px] text-slate-400 font-bold uppercase">Bank-Grade Verification</span>
                                        </div>
                                        {policyForm.require_otp ? <ShieldAlert className="text-rose-600" /> : <Square className="text-slate-300" />}
                                    </div>

                                    <div onClick={() => setPolicyForm({ ...policyForm, require_user_mfa_choice: !policyForm.require_user_mfa_choice })} className={`p-6 rounded-3xl border-2 flex items-center justify-between cursor-pointer transition-all ${policyForm.require_user_mfa_choice ? 'bg-white border-blue-500 shadow-lg' : 'bg-slate-50/50 border-slate-100 opacity-60'}`}>
                                        <div>
                                            <span className="text-xs font-black text-slate-900 block">User-Driven MFA</span>
                                            <span className="text-[9px] text-slate-400 font-bold uppercase">Only ask if User enrolled</span>
                                        </div>
                                        {policyForm.require_user_mfa_choice ? <Fingerprint className="text-blue-600" /> : <Square className="text-slate-300" />}
                                    </div>

                                    <div onClick={() => setPolicyForm({ ...policyForm, auto_login: !policyForm.auto_login })} className={`p-6 rounded-3xl border-2 flex items-center justify-between cursor-pointer transition-all ${policyForm.auto_login ? 'bg-white border-emerald-500 shadow-lg' : 'bg-slate-50/50 border-slate-100 opacity-60'}`}>
                                        <div>
                                            <span className="text-xs font-black text-slate-900 block">Automatic/Direct Login</span>
                                            <span className="text-[9px] text-slate-400 font-bold uppercase">Passwordless Experience</span>
                                        </div>
                                        {policyForm.auto_login ? <Zap className="text-emerald-600" /> : <Square className="text-slate-300" />}
                                    </div>
                                </div>
                            </div>
                        </div>

                        <div className="pt-10 flex justify-end gap-4 border-t border-slate-50 mt-auto">
                            <button onClick={() => setShowPolicyModal(false)} className="px-8 py-4 text-xs font-black text-slate-400 uppercase tracking-widest hover:bg-slate-50 rounded-2xl">Discard Draft</button>
                            <button onClick={handleSavePolicy} className="px-10 py-4 bg-slate-900 text-white rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-600 transition-all">
                                {executing ? <Loader2 className="animate-spin" /> : 'Synchronize Policy'}
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* STRATEGY CONFIG MODAL */}
            {showConfigModal && strategyConfig && (
                <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8 animate-in zoom-in-95">
                    <div className="bg-white rounded-[3.5rem] max-w-2xl w-full p-12 shadow-2xl flex flex-col max-h-[90vh]">
                        <div className="flex justify-between items-center mb-8">
                            <div>
                                <h3 className="text-3xl font-black text-slate-900 capitalize">{selectedStrategy} Settings</h3>
                                <p className="text-slate-400 text-xs font-bold uppercase tracking-widest mt-1">Lifecycle & Security</p>
                            </div>
                            <button onClick={() => setShowConfigModal(false)} className="p-3 bg-slate-50 rounded-full hover:bg-slate-100"><X size={20} /></button>
                        </div>

                        <div className="flex-1 overflow-y-auto space-y-8 pr-2">
                            {/* 1. IDENTIFIER REGEX (Backend Validation) - Placed First */}
                            {!isOauth(selectedStrategy || '') && selectedStrategy !== 'email' && (
                                <div className="bg-slate-50 border border-slate-200 p-6 rounded-3xl space-y-2">
                                    <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest block">Identifier Regex (Backend Validation)</label>
                                    <input
                                        value={strategyConfig.otp_config?.regex_validation || ''}
                                        onChange={(e) => {
                                            const currentOtp = strategyConfig.otp_config || { length: 6, charset: 'numeric' };
                                            setStrategyConfig({
                                                ...strategyConfig,
                                                otp_config: { ...currentOtp, regex_validation: e.target.value }
                                            });
                                        }}
                                        placeholder="e.g. ^\d{11}$ (CPF)"
                                        className="w-full mt-1 bg-white border border-slate-200 rounded-xl py-3 px-4 text-xs font-mono font-bold outline-none"
                                    />
                                </div>
                            )}

                            {/* 2. LIFECYCLE PARAMETERS (Refresh Validity & JWT Expiration) */}
                            <div className="grid grid-cols-2 gap-6">
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Refresh Validity (Days)</label>
                                    <input
                                        type="number"
                                        value={strategyConfig.refresh_validity_days || 30}
                                        onChange={(e) => setStrategyConfig({ ...strategyConfig, refresh_validity_days: parseInt(e.target.value) })}
                                        className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-sm font-bold outline-none"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">JWT Expiration</label>
                                    <input
                                        value={strategyConfig.jwt_expiration || '24h'}
                                        onChange={(e) => setStrategyConfig({ ...strategyConfig, jwt_expiration: e.target.value })}
                                        className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-sm font-bold outline-none"
                                    />
                                </div>
                            </div>

                            {/* Allowed Authentication Methods */}
                            {!isOauth(selectedStrategy || '') && (
                                <div className="bg-slate-50 border border-slate-200 p-6 rounded-[2rem] space-y-4">
                                    <h5 className="font-black text-slate-700 text-xs uppercase tracking-widest flex items-center gap-2"><Lock size={12} /> Allowed Authentication Methods</h5>
                                    
                                    <div className="space-y-3">
                                        {/* Password Switch */}
                                        <div className="flex justify-between items-center bg-white p-4 rounded-2xl border border-slate-100 shadow-sm">
                                            <div>
                                                <h6 className="font-bold text-slate-800 text-xs">Password Authentication</h6>
                                                <p className="text-[9px] text-slate-400 mt-0.5">Allow login using traditional text passwords.</p>
                                            </div>
                                            <label className="relative inline-flex items-center cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    className="sr-only peer"
                                                    checked={strategyConfig.password_enabled !== false}
                                                    onChange={(e) => setStrategyConfig({ ...strategyConfig, password_enabled: e.target.checked })}
                                                />
                                                <div className="w-10 h-5 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-slate-900"></div>
                                            </label>
                                        </div>

                                        {/* Magic Link Switch */}
                                        <div className="flex justify-between items-center bg-white p-4 rounded-2xl border border-slate-100 shadow-sm">
                                            <div>
                                                <h6 className="font-bold text-slate-800 text-xs">Magic Link</h6>
                                                <p className="text-[9px] text-slate-400 mt-0.5">Allow login using a clickable link sent to the identifier.</p>
                                            </div>
                                            <label className="relative inline-flex items-center cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    className="sr-only peer"
                                                    checked={strategyConfig.magiclink_enabled !== false}
                                                    onChange={(e) => setStrategyConfig({ ...strategyConfig, magiclink_enabled: e.target.checked })}
                                                />
                                                <div className="w-10 h-5 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-purple-600"></div>
                                            </label>
                                        </div>

                                        {/* OTP Switch */}
                                        <div className="flex justify-between items-center bg-white p-4 rounded-2xl border border-slate-100 shadow-sm">
                                            <div>
                                                <h6 className="font-bold text-slate-800 text-xs">Passwordless OTP</h6>
                                                <p className="text-[9px] text-slate-400 mt-0.5">Allow login using a verification code dispatched to the identifier.</p>
                                            </div>
                                            <label className="relative inline-flex items-center cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    className="sr-only peer"
                                                    checked={strategyConfig.otp_enabled !== false}
                                                    onChange={(e) => setStrategyConfig({ ...strategyConfig, otp_enabled: e.target.checked })}
                                                />
                                                <div className="w-10 h-5 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-indigo-600"></div>
                                            </label>
                                        </div>

                                        {/* Biometrics Switch */}
                                        <div className="flex justify-between items-center bg-white p-4 rounded-2xl border border-slate-100 shadow-sm">
                                            <div>
                                                <h6 className="font-bold text-slate-800 text-xs">Biometric Authentication (Passkey)</h6>
                                                <p className="text-[9px] text-slate-400 mt-0.5">Allow login using hardware security keys or biometrics (Face ID/Touch ID).</p>
                                            </div>
                                            <label className="relative inline-flex items-center cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    className="sr-only peer"
                                                    checked={!!strategyConfig.biometria_enabled}
                                                    onChange={(e) => setStrategyConfig({ ...strategyConfig, biometria_enabled: e.target.checked })}
                                                />
                                                <div className="w-10 h-5 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-emerald-600"></div>
                                            </label>
                                        </div>

                                        {/* TOTP / MFA Switch */}
                                        <div className="flex justify-between items-center bg-white p-4 rounded-2xl border border-slate-100 shadow-sm">
                                            <div>
                                                <h6 className="font-bold text-slate-800 text-xs">TOTP / Authenticator App (MFA)</h6>
                                                <p className="text-[9px] text-slate-400 mt-0.5">Support Microsoft/Google Authenticator as a supplementary or primary factor.</p>
                                            </div>
                                            <label className="relative inline-flex items-center cursor-pointer">
                                                <input
                                                    type="checkbox"
                                                    className="sr-only peer"
                                                    checked={!!strategyConfig.totp_enabled}
                                                    onChange={(e) => setStrategyConfig({ ...strategyConfig, totp_enabled: e.target.checked })}
                                                />
                                                <div className="w-10 h-5 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-amber-600"></div>
                                            </label>
                                        </div>
                                    </div>
                                </div>
                            )}

                            {/* 3. CUSTOM OTP CONFIGURATION */}
                            {!isOauth(selectedStrategy || '') && strategyConfig.otp_enabled !== false && (
                                <div className="bg-indigo-50 border border-indigo-100 p-6 rounded-3xl space-y-4">
                                    <div className="flex items-center justify-between">
                                        <div>
                                            <h5 className="font-bold text-indigo-900 text-sm flex items-center gap-2"><Hash size={14} /> Custom OTP Configuration</h5>
                                            <p className="text-[10px] text-indigo-700 font-bold mt-1">Enable OTP challenges for user sign-in and verification</p>
                                        </div>
                                        <label className="relative inline-flex items-center cursor-pointer">
                                            <input
                                                type="checkbox"
                                                className="sr-only peer"
                                                checked={!!strategyConfig.otp_config}
                                                onChange={(e) => {
                                                    if (e.target.checked) {
                                                        setStrategyConfig({
                                                            ...strategyConfig,
                                                            otp_config: {
                                                                length: strategyConfig.otp_config?.length || 6,
                                                                charset: strategyConfig.otp_config?.charset || 'numeric',
                                                                regex_validation: strategyConfig.otp_config?.regex_validation || ''
                                                            }
                                                        });
                                                    } else {
                                                        setStrategyConfig({
                                                            ...strategyConfig,
                                                            otp_config: undefined
                                                        });
                                                    }
                                                }}
                                            />
                                            <div className="w-11 h-6 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-indigo-600"></div>
                                        </label>
                                    </div>

                                    {strategyConfig.otp_config && (
                                        <div className="grid grid-cols-2 gap-4 pt-4 border-t border-indigo-200/50 animate-in fade-in duration-200">
                                            <div>
                                                <label className="text-[9px] font-black text-indigo-400 uppercase tracking-widest">Code Length</label>
                                                <input
                                                    type="number"
                                                    value={strategyConfig.otp_config?.length || 6}
                                                    onChange={(e) => setStrategyConfig({ ...strategyConfig, otp_config: { ...strategyConfig.otp_config, length: parseInt(e.target.value) } })}
                                                    className="w-full mt-1 bg-white border border-slate-200 rounded-xl py-2 px-3 text-xs font-bold outline-none"
                                                />
                                            </div>
                                            <div>
                                                <label className="text-[9px] font-black text-indigo-400 uppercase tracking-widest">Charset</label>
                                                <select
                                                    value={strategyConfig.otp_config?.charset || 'numeric'}
                                                    onChange={(e) => setStrategyConfig({ ...strategyConfig, otp_config: { ...strategyConfig.otp_config, charset: e.target.value } })}
                                                    className="w-full mt-1 bg-white border border-slate-200 rounded-xl py-2 px-3 text-xs font-bold outline-none shadow-sm"
                                                >
                                                    <option value="numeric">Numeric (0-9)</option>
                                                    <option value="alphanumeric">Alphanumeric (A-Z, 0-9)</option>
                                                    <option value="alpha">Alpha (A-Z)</option>
                                                    <option value="hex">Hex (0-9, A-F)</option>
                                                </select>
                                            </div>
                                            <div className="col-span-2 space-y-2 pt-2">
                                                <label className="text-[9px] font-black text-indigo-400 uppercase tracking-widest block">OTP Delivery Method</label>
                                                <div className="grid grid-cols-2 gap-2 mb-3">
                                                    {[
                                                        { id: 'smtp', label: '📧 SMTP Server', value: 'smtp://' },
                                                        { id: 'resend', label: '🚀 Resend API', value: 'resend://' },
                                                        { id: 'webhook', label: '🔗 Webhook URL', value: 'webhook' },
                                                        { id: 'nexus', label: '⚡ Nexus Automation', value: 'nexus://' }
                                                    ].map((opt) => {
                                                        const isChecked = opt.id === 'nexus'
                                                            ? (strategyConfig.webhook_url || '').startsWith('nexus://')
                                                            : opt.id === 'smtp'
                                                                ? strategyConfig.webhook_url === 'smtp://'
                                                                : opt.id === 'resend'
                                                                    ? strategyConfig.webhook_url === 'resend://'
                                                                    : (strategyConfig.webhook_url && !strategyConfig.webhook_url.startsWith('nexus://') && strategyConfig.webhook_url !== 'smtp://' && strategyConfig.webhook_url !== 'resend://');

                                                        return (
                                                            <button
                                                                key={opt.id}
                                                                type="button"
                                                                onClick={() => {
                                                                    if (opt.id === 'smtp') {
                                                                        setStrategyConfig({ ...strategyConfig, webhook_url: 'smtp://' });
                                                                    } else if (opt.id === 'resend') {
                                                                        setStrategyConfig({ ...strategyConfig, webhook_url: 'resend://' });
                                                                    } else if (opt.id === 'nexus') {
                                                                        const firstAuto = automations[0]?.id || '';
                                                                        setStrategyConfig({ ...strategyConfig, webhook_url: firstAuto ? `nexus://${firstAuto}` : 'nexus://' });
                                                                    } else {
                                                                        setStrategyConfig({ ...strategyConfig, webhook_url: '' });
                                                                    }
                                                                }}
                                                                className={`py-2.5 px-3 rounded-xl border text-[10px] font-bold text-left transition-all ${
                                                                    isChecked
                                                                        ? 'bg-indigo-600 border-indigo-600 text-white shadow-sm'
                                                                        : 'bg-white border-slate-200 text-slate-600 hover:bg-slate-50'
                                                                }`}
                                                            >
                                                                {opt.label}
                                                            </button>
                                                        );
                                                    })}
                                                </div>

                                                {((strategyConfig.webhook_url || '').startsWith('nexus://')) && (
                                                    <div className="mt-2 animate-in fade-in duration-200">
                                                        <label className="text-[9px] font-black text-indigo-400 uppercase tracking-widest">Select Nexus Automation</label>
                                                        <select
                                                            value={(strategyConfig.webhook_url || '').replace('nexus://', '')}
                                                            onChange={(e) => setStrategyConfig({ ...strategyConfig, webhook_url: `nexus://${e.target.value}` })}
                                                            className="w-full mt-1 bg-white border border-slate-200 rounded-xl py-2 px-3 text-xs font-bold outline-none shadow-sm"
                                                        >
                                                            <option value="">Choose an Automation</option>
                                                            {automations.map((a: any) => (
                                                                <option key={a.id} value={a.id}>{a.name} ({a.trigger_type || 'Workflow'})</option>
                                                            ))}
                                                        </select>
                                                    </div>
                                                )}

                                                {(!strategyConfig.webhook_url || (!strategyConfig.webhook_url.startsWith('nexus://') && strategyConfig.webhook_url !== 'smtp://' && strategyConfig.webhook_url !== 'resend://')) && (
                                                    <div className="mt-2 animate-in fade-in duration-200">
                                                        <label className="text-[9px] font-black text-indigo-400 uppercase tracking-widest">OTP Webhook URL</label>
                                                        <input
                                                            value={strategyConfig.webhook_url || ''}
                                                            onChange={(e) => setStrategyConfig({ ...strategyConfig, webhook_url: e.target.value })}
                                                            placeholder="https://n8n.webhook/send-otp"
                                                            className="w-full mt-1 bg-white border border-slate-200 rounded-xl py-2.5 px-3 text-xs font-bold outline-none"
                                                        />
                                                    </div>
                                                )}

                                                {strategyConfig.webhook_url === 'smtp://' && (
                                                    <div className="mt-2 p-3 bg-white/50 border border-indigo-100/50 rounded-xl text-[10px] text-slate-500 italic">
                                                        OTP codes will be dispatched using the SMTP settings configured in your SMTP Email Gateway.
                                                    </div>
                                                )}

                                                {strategyConfig.webhook_url === 'resend://' && (
                                                    <div className="mt-2 p-3 bg-white/50 border border-indigo-100/50 rounded-xl text-[10px] text-slate-500 italic">
                                                        OTP codes will be dispatched using the API key configured in your Resend Email Gateway.
                                                    </div>
                                                )}
                                            </div>
                                        </div>
                                    )}
                                </div>
                            )}

                            {/* 4. BANK-GRADE SECURITY (OTP ENFORCEMENT) - Only if Custom OTP is enabled */}
                            {strategyConfig.otp_enabled !== false && strategyConfig.otp_config && (
                                <div className="bg-rose-50 border border-rose-100 p-6 rounded-3xl space-y-4 animate-in fade-in duration-200">
                                    <div className="flex items-center justify-between">
                                        <div>
                                            <h5 className="font-bold text-rose-900 text-sm flex items-center gap-2">🔐 Bank-Grade Security Lock</h5>
                                            <p className="text-[10px] text-rose-700 font-bold mt-1">Require OTP challenge for sensitive updates (e.g., password, new identity)</p>
                                        </div>
                                        <label className="relative inline-flex items-center cursor-pointer">
                                            <input
                                                type="checkbox"
                                                className="sr-only peer"
                                                checked={strategyConfig.require_otp_on_update || false}
                                                onChange={(e) => setStrategyConfig({ ...strategyConfig, require_otp_on_update: e.target.checked })}
                                            />
                                            <div className="w-11 h-6 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-rose-600"></div>
                                        </label>
                                    </div>

                                    {strategyConfig.require_otp_on_update && (
                                        <div className="pt-4 border-t border-rose-200/50">
                                            <label className="text-[9px] font-black text-rose-800 uppercase tracking-widest">OTP Dispatch Mode</label>
                                            <select
                                                value={strategyConfig.otp_dispatch_mode || 'delegated'}
                                                onChange={(e) => setStrategyConfig({ ...strategyConfig, otp_dispatch_mode: e.target.value })}
                                                className="w-full mt-2 bg-white border-none rounded-xl py-3 px-4 text-xs font-bold outline-none text-slate-700 shadow-sm"
                                            >
                                                <option value="delegated">Delegated (Frontend prompts User to choose Channel)</option>
                                                <option value="auto_current">Auto-Current (Send OTP to the Identity being updated)</option>
                                                <option value="auto_primary">Auto-Primary (Send OTP to Account's root email)</option>
                                            </select>
                                            <p className="text-[9px] text-rose-600/70 font-semibold mt-2">
                                                Determines how the API routes the security code when an update is blocked.
                                            </p>
                                        </div>
                                    )}
                                </div>
                            )}

                            {/* 5. AUTHORIZED ORIGINS */}
                            <div className="space-y-3">
                                <div className="flex flex-col gap-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Authorized Origins (CORS/Redirects)</label>
                                    <div className="flex gap-2">
                                        <input
                                            value={strategyConfig.newRule || ''}
                                            onChange={(e) => setStrategyConfig({ ...strategyConfig, newRule: e.target.value })}
                                            placeholder="https://meu-app.com"
                                            className="flex-1 bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-mono font-bold outline-none focus:ring-2 focus:ring-indigo-500/20"
                                            onKeyDown={(e) => {
                                                if (e.key === 'Enter') {
                                                    addRuleToStrategy(strategyConfig.newRule || '');
                                                }
                                            }}
                                        />
                                        <button onClick={() => {
                                            addRuleToStrategy(strategyConfig.newRule || '');
                                        }} className="px-5 py-3 bg-indigo-50 text-indigo-600 font-bold text-[10px] uppercase rounded-xl hover:bg-indigo-100 transition-colors shrink-0">Add Origin</button>
                                    </div>
                                </div>
                                <div className="space-y-2">
                                    {strategyConfig.rules?.map((rule: any, idx: number) => (
                                        <div key={idx} className="bg-slate-50 p-6 rounded-[2rem] border border-slate-100 flex flex-col gap-4 animate-in fade-in slide-in-from-top-2">
                                            <div className="flex items-center justify-between">
                                                <div className="flex items-center gap-3">
                                                    <div className="w-8 h-8 bg-indigo-100 text-indigo-600 rounded-xl flex items-center justify-center"><Globe size={14} /></div>
                                                    <span className="text-xs font-mono font-black text-slate-800">{rule.origin}</span>
                                                </div>
                                                <button onClick={() => removeRuleFromStrategy(rule.origin)} className="p-2 text-slate-300 hover:text-rose-600 transition-colors"><X size={16} /></button>
                                            </div>

                                            <div className="grid grid-cols-4 gap-2">
                                                <button
                                                    onClick={() => {
                                                        const newRules = [...strategyConfig.rules];
                                                        newRules[idx] = { ...newRules[idx], require_password: !newRules[idx].require_password };
                                                        setStrategyConfig({ ...strategyConfig, rules: newRules });
                                                    }}
                                                    className={`py-2 px-3 rounded-xl border text-[9px] font-black uppercase transition-all flex flex-col items-center gap-1.5 ${rule.require_password ? 'bg-indigo-600 text-white shadow-md border-indigo-500' : 'bg-white text-slate-400 border-slate-200'}`}
                                                >
                                                    <Key size={12} /> Senha
                                                </button>
                                                <button
                                                    onClick={() => {
                                                        const newRules = [...strategyConfig.rules];
                                                        newRules[idx] = { ...newRules[idx], require_otp: !newRules[idx].require_otp };
                                                        setStrategyConfig({ ...strategyConfig, rules: newRules });
                                                    }}
                                                    className={`py-2 px-3 rounded-xl border text-[9px] font-black uppercase transition-all flex flex-col items-center gap-1.5 ${rule.require_otp ? 'bg-orange-500 text-white shadow-md border-orange-400' : 'bg-white text-slate-400 border-slate-200'}`}
                                                >
                                                    <Smartphone size={12} /> OTP
                                                </button>
                                                <button
                                                    onClick={() => {
                                                        const newRules = [...strategyConfig.rules];
                                                        newRules[idx] = { ...newRules[idx], require_totp: !newRules[idx].require_totp };
                                                        setStrategyConfig({ ...strategyConfig, rules: newRules });
                                                    }}
                                                    className={`py-2 px-3 rounded-xl border text-[9px] font-black uppercase transition-all flex flex-col items-center gap-1.5 ${rule.require_totp ? 'bg-purple-600 text-white shadow-md border-purple-500' : 'bg-white text-slate-400 border-slate-200'}`}
                                                >
                                                    <Clock size={12} /> TOTP
                                                </button>
                                                <button
                                                    onClick={() => {
                                                        const newRules = [...strategyConfig.rules];
                                                        newRules[idx] = { ...newRules[idx], auto_login: !newRules[idx].auto_login };
                                                        setStrategyConfig({ ...strategyConfig, rules: newRules });
                                                    }}
                                                    className={`py-2 px-3 rounded-xl border text-[9px] font-black uppercase transition-all flex flex-col items-center gap-1.5 ${rule.auto_login ? 'bg-emerald-500 text-white shadow-md border-emerald-400' : 'bg-white text-slate-400 border-slate-200'}`}
                                                >
                                                    <Zap size={12} /> Auto
                                                </button>
                                            </div>
                                        </div>
                                    ))}
                                    {(!strategyConfig.rules || strategyConfig.rules.length === 0) && <p className="text-xs text-slate-400 italic">No origin rules defined (Public).</p>}
                                </div>
                            </div>

                            {/* 6. TEMPLATE BINDING (i18n) - Moved to the end */}
                            {!isOauth(selectedStrategy || '') && Object.keys(messagingTemplates).length > 0 && strategyConfig.otp_config && (
                                <div className="bg-indigo-50/50 border border-indigo-100/50 p-6 rounded-3xl space-y-3">
                                    <label className="text-[9px] font-black text-indigo-400 uppercase tracking-widest block">OTP Message Template (i18n Library)</label>
                                    <select
                                        value={strategyConfig.template_bindings?.otp_challenge || ''}
                                        onChange={(e) => setStrategyConfig({
                                            ...strategyConfig,
                                            template_bindings: { ...(strategyConfig.template_bindings || {}), otp_challenge: e.target.value || undefined }
                                        })}
                                        className="w-full bg-white border border-slate-200 rounded-xl py-2.5 px-3 text-xs font-bold outline-none shadow-sm"
                                    >
                                        <option value="">System Default (No i18n)</option>
                                        {Object.values(messagingTemplates)
                                            .filter((t: any) => t.type === 'otp_challenge')
                                            .map((t: any) => (
                                                <option key={t.id} value={t.id}>{t.name} ({Object.keys(t.variants).join(', ')})</option>
                                            ))
                                        }
                                    </select>
                                    {strategyConfig.template_bindings?.otp_challenge && messagingTemplates[strategyConfig.template_bindings.otp_challenge] && (
                                        <div className="bg-white/80 rounded-xl p-3 border border-indigo-100">
                                            <p className="text-[9px] font-black text-indigo-500 uppercase tracking-widest mb-1">
                                                Preview ({messagingTemplates[strategyConfig.template_bindings.otp_challenge].default_language})
                                            </p>
                                            <p className="text-[10px] text-slate-600 font-mono leading-relaxed whitespace-pre-wrap max-h-20 overflow-y-auto">
                                                {messagingTemplates[strategyConfig.template_bindings.otp_challenge].variants?.[messagingTemplates[strategyConfig.template_bindings.otp_challenge].default_language]?.body || '(empty body)'}
                                            </p>
                                        </div>
                                    )}
                                </div>
                            )}

                            {/* 7. EDUCATIONAL SNIPPET FOR CUSTOM STRATEGIES - Moved to the end */}
                            {!isOauth(selectedStrategy || '') && selectedStrategy !== 'email' && (
                                <div className="bg-slate-900 rounded-2xl p-6 border border-slate-800 shadow-xl overflow-hidden relative group">
                                    <div className="flex justify-between items-center mb-4">
                                        <h4 className="text-emerald-400 font-black text-xs uppercase tracking-widest flex items-center gap-2"><Code size={14} /> Integration Snippet</h4>
                                        <button onClick={() => safeCopy(`
// Universal Login (Any Provider)
const { user, session } = await cascata.auth.signIn({
  provider: '${selectedStrategy}',
  identifier: 'unique_user_id',
  password: 'user_password'
});`)} className="text-slate-500 hover:text-white transition-colors p-1"><Copy size={14} /></button>
                                    </div>
                                    <pre className="text-[10px] font-mono text-slate-300 whitespace-pre-wrap leading-relaxed">
                                        {`// Universal Login (Any Provider)
const { user, session } = await cascata.auth.signIn({
  provider: '${selectedStrategy}',
  identifier: 'unique_user_id',
  password: 'user_password'
});`}
                                    </pre>
                                    <div className="mt-4 p-3 bg-white/5 rounded-xl border border-white/10 text-[10px] text-slate-400 leading-relaxed">
                                        Use <strong>Universal Login</strong> to authenticate with this custom strategy.
                                        Unlike standard email login, this endpoint accepts any provider identifier you define.
                                    </div>
                                </div>
                            )}
                        </div>

                        <div className="pt-8 border-t border-slate-100 flex justify-end gap-4 mt-auto">
                            <button onClick={() => setShowConfigModal(false)} className="px-6 py-4 rounded-2xl text-xs font-black text-slate-400 uppercase tracking-widest hover:bg-slate-50">Cancel</button>
                            <button onClick={handleSaveStrategyConfig} className="px-8 py-4 bg-indigo-600 text-white rounded-2xl text-xs font-black uppercase tracking-widest shadow-xl hover:bg-indigo-700">Save Changes</button>
                        </div>
                    </div>
                </div>
            )
            }

            {/* CREATE USER MODAL */}
            {
                showCreateUser && (
                    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8 animate-in zoom-in-95">
                        <div className="bg-white rounded-[3rem] w-full max-w-md p-10 shadow-2xl relative">
                            <button onClick={() => setShowCreateUser(false)} className="absolute top-8 right-8 text-slate-300 hover:text-slate-900"><X size={24} /></button>
                            <h3 className="text-2xl font-black text-slate-900 mb-6">Create User</h3>
                            <div className="space-y-4">
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Identifier (Email/Phone)</label>
                                    <input value={createUserForm.identifier} onChange={(e) => setCreateUserForm({ ...createUserForm, identifier: e.target.value })} className="w-full bg-slate-50 border-none rounded-2xl py-3 px-4 text-sm font-bold outline-none" placeholder="user@example.com" />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Password</label>
                                    <input type="password" value={createUserForm.password} onChange={(e) => setCreateUserForm({ ...createUserForm, password: e.target.value })} className="w-full bg-slate-50 border-none rounded-2xl py-3 px-4 text-sm font-bold outline-none" placeholder="••••••••" />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Provider</label>
                                    <select value={createUserForm.provider} onChange={(e) => setCreateUserForm({ ...createUserForm, provider: e.target.value })} className="w-full bg-slate-50 border-none rounded-2xl py-3 px-4 text-sm font-bold outline-none">
                                        {Object.keys(strategies).map((providerKey) => (
                                            <option key={providerKey} value={providerKey}>
                                                {providerKey.charAt(0).toUpperCase() + providerKey.slice(1)}
                                            </option>
                                        ))}
                                    </select>
                                </div>
                                <button onClick={handleCreateUser} disabled={executing} className="w-full bg-indigo-600 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl mt-4 hover:bg-indigo-700 transition-all">
                                    {executing ? <Loader2 className="animate-spin mx-auto" /> : 'Create User'}
                                </button>
                            </div>
                        </div>
                    </div>
                )
            }

            {/* NEW STRATEGY MODAL */}
            {
                showNewStrategy && (
                    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8 animate-in zoom-in-95">
                        <div className="bg-white rounded-[3rem] w-full max-w-sm p-10 shadow-2xl relative">
                            <h3 className="text-xl font-black text-slate-900 mb-2">Criar Estratégia Customizada</h3>
                            <p className="text-xs text-slate-400 font-bold mb-5">Cadastre um Custom Challenge Provider para Step-Up, como Z-API WhatsApp. O provider ficará disponível para os cadeados do banco.</p>
                            <input autoFocus value={newStrategyName} onChange={(e) => setNewStrategyName(e.target.value)} placeholder="Z-API WhatsApp" className="w-full bg-slate-50 border-none rounded-2xl py-3 px-4 text-sm font-bold outline-none mb-3" />
                            <p className="text-[10px] text-slate-400 font-mono mb-6">factor_id: {newStrategyName ? newStrategyName.toLowerCase().replace(/[^a-z0-9_]/g, '_') : 'z_api_whatsapp'}</p>
                            <div className="flex gap-4">
                                <button onClick={() => setShowNewStrategy(false)} className="flex-1 py-3 text-slate-400 font-bold text-xs uppercase hover:bg-slate-50 rounded-xl">Cancel</button>
                                <button onClick={handleCreateCustomStrategy} className="flex-1 py-3 bg-indigo-600 text-white rounded-xl font-bold text-xs uppercase shadow-lg hover:bg-indigo-700">Create</button>
                            </div>
                        </div>
                    </div>
                )
            }

            {/* PROVIDER CONFIG MODAL */}
            {
                showProviderConfig && (
                    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8 animate-in zoom-in-95">
                        <div className="bg-white rounded-[3rem] w-full max-w-md p-10 shadow-2xl relative">
                            <button onClick={() => setShowProviderConfig(null)} className="absolute top-8 right-8 text-slate-300 hover:text-slate-900 transition-colors"><X size={24} /></button>
                            <div className="flex flex-col items-center mb-6">
                                <div className="w-16 h-16 bg-slate-900 text-white rounded-[1.5rem] flex items-center justify-center shadow-xl mb-4">
                                    {showProviderConfig === 'github' ? <Github size={32} /> : <Globe size={32} />}
                                </div>
                                <h3 className="text-2xl font-black text-slate-900 tracking-tight capitalize">Configure {showProviderConfig}</h3>
                                <p className="text-xs text-slate-400 font-bold uppercase tracking-widest mt-1">OAuth Integration</p>
                            </div>

                            <div className="space-y-4">
                                <div className="space-y-1">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Client ID</label>
                                    <input
                                        value={providerConfig.client_id || ''}
                                        onChange={(e) => setProviderConfig({ ...providerConfig, client_id: e.target.value })}
                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none focus:ring-4 focus:ring-indigo-500/10 transition-all font-mono text-indigo-600"
                                        placeholder="Received from Provider"
                                    />
                                </div>
                                <div className="space-y-1">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Client Secret</label>
                                    <input
                                        type="password"
                                        value={providerConfig.client_secret || ''}
                                        onChange={(e) => setProviderConfig({ ...providerConfig, client_secret: e.target.value })}
                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none focus:ring-4 focus:ring-indigo-500/10 transition-all font-mono"
                                        placeholder="••••••••••••••••"
                                    />
                                </div>

                                <div className="p-4 bg-slate-50 border border-slate-100 rounded-2xl">
                                    <label className="text-[9px] font-black text-slate-400 uppercase tracking-widest mb-2 block flex items-center gap-1"><Link size={10} /> Callback URL (Redirect URI)</label>
                                    <div className="flex items-center gap-2 bg-white border border-slate-200 p-2 rounded-xl">
                                        <code className="text-[10px] text-slate-600 font-mono truncate flex-1">{getCallbackUrl()}</code>
                                        <button onClick={() => safeCopy(getCallbackUrl())} className="p-1.5 hover:bg-slate-100 rounded-lg text-indigo-500"><Copy size={12} /></button>
                                    </div>
                                    <p className="text-[9px] text-slate-400 mt-2 px-1 leading-tight">
                                        Add this URL to your OAuth App settings in the Provider's Developer Console.
                                    </p>
                                </div>

                                <div className="p-4 bg-slate-50 border border-slate-100 rounded-2xl">
                                    <label className="flex items-center gap-3 cursor-pointer">
                                        <input
                                            type="checkbox"
                                            checked={providerConfig.auto_verify || false}
                                            onChange={(e) => setProviderConfig({ ...providerConfig, auto_verify: e.target.checked })}
                                            className="w-4 h-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                                        />
                                        <div>
                                            <span className="text-xs font-bold text-slate-700">Auto-Verify Identities</span>
                                            <p className="text-[9px] text-slate-400 leading-tight mt-0.5">
                                                Automatically mark identities from this provider as verified upon account creation.
                                            </p>
                                        </div>
                                    </label>
                                </div>

                                <button onClick={handleSaveProviderConfig} className="w-full bg-indigo-600 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-700 transition-all flex items-center justify-center gap-2 mt-2">
                                    <CheckCircle2 size={16} /> Save Configuration
                                </button>
                            </div>
                        </div>
                    </div>
                )
            }

            {/* DELETE CONFIRM */}
            {
                showDeleteModal && (
                    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[600] flex items-center justify-center p-8 animate-in zoom-in-95">
                        <div className="bg-white rounded-[3rem] w-full max-w-sm p-10 shadow-2xl text-center border border-rose-100">
                            <AlertCircle size={48} className="text-rose-500 mx-auto mb-4" />
                            <h3 className="text-xl font-black text-slate-900 mb-2">Delete User?</h3>
                            <p className="text-xs text-slate-500 mb-6">To confirm, type the User UUID below.</p>
                            <code className="block bg-slate-100 p-2 rounded-lg text-[10px] font-mono mb-4 text-slate-600 select-all">{showDeleteModal.id}</code>
                            <input value={deleteConfirmUuid} onChange={(e) => setDeleteConfirmUuid(e.target.value)} className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold text-center outline-none mb-6 focus:ring-4 focus:ring-rose-500/10" />
                            <div className="flex gap-4">
                                <button onClick={() => setShowDeleteModal(null)} className="flex-1 py-3 text-slate-400 font-bold text-xs uppercase hover:bg-slate-50 rounded-xl">Cancel</button>
                                <button onClick={handleDeleteUser} disabled={deleteConfirmUuid !== showDeleteModal.id || executing} className="flex-1 py-3 bg-rose-600 text-white rounded-xl font-bold text-xs uppercase shadow-lg hover:bg-rose-700 disabled:opacity-50">Delete</button>
                            </div>
                        </div>
                    </div>
                )
            }

            {/* USER DETAIL MODAL (LINK IDENTITIES) */}
            {
                showUserModal && selectedUser && (
                    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8 animate-in zoom-in-95">
                        <div className="bg-white rounded-[3.5rem] w-full max-w-2xl p-12 shadow-2xl border border-slate-100 flex flex-col max-h-[90vh]">
                            <header className="flex justify-between items-start mb-8">
                                <div>
                                    <h3 className="text-3xl font-black text-slate-900">User Details</h3>
                                    <div className="flex items-center gap-2 mt-2">
                                        <span className={`px-2 py-0.5 rounded text-[10px] font-black uppercase tracking-widest ${selectedUser.banned ? 'bg-rose-100 text-rose-600' : 'bg-emerald-100 text-emerald-600'}`}>{selectedUser.banned ? 'Banned' : 'Active'}</span>
                                        <span className="text-xs text-slate-400 font-mono">{selectedUser.id}</span>
                                    </div>
                                </div>
                                <button onClick={() => setShowUserModal(false)} className="p-2 hover:bg-slate-100 rounded-full text-slate-400"><X size={24} /></button>
                            </header>

                            <div className="flex-1 overflow-y-auto space-y-8">
                                {/* IDENTITIES LIST */}
                                <div className="space-y-4">
                                    <div className="flex justify-between items-center">
                                        <div>
                                            <h4 className="text-sm font-black text-slate-900">Identity Map</h4>
                                            <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-1">User Identity Mesh linked to this account</p>
                                        </div>
                                        <button onClick={() => setShowLinkIdentity(true)} className="text-[10px] font-bold text-indigo-600 uppercase hover:bg-indigo-50 px-3 py-1.5 rounded-lg transition-all flex items-center gap-1"><Plus size={12} /> Link New</button>
                                    </div>

                                    <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
                                        {(selectedUser.identities || []).map((id: any) => (
                                            <div key={`map-${id.id || id.provider}-${id.identifier}`} className={`p-4 rounded-2xl border ${id.verified_at ? 'bg-emerald-50 border-emerald-100' : 'bg-amber-50 border-amber-100'}`}>
                                                <div className="flex items-center justify-between gap-2">
                                                    <div className={`w-9 h-9 rounded-xl flex items-center justify-center ${id.verified_at ? 'bg-emerald-600 text-white' : 'bg-amber-500 text-white'}`}>
                                                        {getIdentityIcon(id.provider)}
                                                    </div>
                                                    {id.verified_at ? <CheckCircle2 size={14} className="text-emerald-600" /> : <AlertCircle size={14} className="text-amber-600" />}
                                                </div>
                                                <p className="mt-3 text-[10px] font-black uppercase tracking-widest text-slate-700">{formatProviderName(id.provider)}</p>
                                                <p className={`mt-1 text-[10px] font-mono truncate ${isSensitiveVisible ? 'text-slate-500' : 'text-slate-400 blur-[3px] select-none'}`} title={id.identifier}>
                                                    {id.identifier}
                                                </p>
                                            </div>
                                        ))}
                                        {(!selectedUser.identities || selectedUser.identities.length === 0) && (
                                            <div className="col-span-full p-6 rounded-2xl border border-dashed border-slate-200 text-center text-xs text-slate-400 font-bold">
                                                Nenhuma identidade vinculada.
                                            </div>
                                        )}
                                    </div>

                                    {showLinkIdentity && (
                                        <div className="bg-slate-50 p-4 rounded-2xl border border-slate-200 animate-in slide-in-from-top-2 space-y-3">
                                            <div className="grid grid-cols-3 gap-3">
                                                <select value={linkIdentityForm.provider} onChange={e => setLinkIdentityForm({ ...linkIdentityForm, provider: e.target.value })} className="bg-white border-none rounded-xl py-2 px-3 text-xs font-bold outline-none">
                                                    {Object.keys(strategies).filter(k => strategies[k].enabled).map(k => <option key={k} value={k}>{k}</option>)}
                                                </select>
                                                <input value={linkIdentityForm.identifier} onChange={e => setLinkIdentityForm({ ...linkIdentityForm, identifier: e.target.value })} placeholder="Identifier" className="col-span-2 bg-white border-none rounded-xl py-2 px-3 text-xs font-bold outline-none" />
                                            </div>
                                            <input type="password" value={linkIdentityForm.password} onChange={e => setLinkIdentityForm({ ...linkIdentityForm, password: e.target.value })} placeholder="Password (Optional)" className="w-full bg-white border-none rounded-xl py-2 px-3 text-xs font-bold outline-none" />
                                            <div className="flex gap-2 justify-end">
                                                <button onClick={() => setShowLinkIdentity(false)} className="text-[10px] font-bold text-slate-400 px-3 py-2">Cancel</button>
                                                <button onClick={handleLinkIdentity} className="bg-indigo-600 text-white px-4 py-2 rounded-xl text-[10px] font-bold uppercase hover:bg-indigo-700">Link</button>
                                            </div>
                                        </div>
                                    )}

                                    <div className="space-y-2">
                                        {selectedUser.identities?.map((id: any) => (
                                            <div key={id.id} className="flex items-center justify-between bg-slate-50 p-4 rounded-2xl border border-slate-100">
                                                <div className="flex items-center gap-3">
                                                    <div className="w-8 h-8 bg-white rounded-lg flex items-center justify-center shadow-sm text-indigo-600">
                                                        {getIdentityIcon(id.provider)}
                                                    </div>
                                                    <div>
                                                        <p className="text-xs font-bold text-slate-700">{id.identifier}</p>
                                                        <p className="text-[10px] text-slate-400 font-bold uppercase">{formatProviderName(id.provider)}</p>
                                                    </div>
                                                </div>
                                                <button onClick={() => handleUnlinkIdentity(id.id)} className="p-2 text-slate-300 hover:text-rose-600 hover:bg-white rounded-lg transition-all" title="Unlink"><Unlink size={16} /></button>
                                                {id.verified_at ? (
                                                    <span className="text-[9px] font-black text-emerald-500 bg-emerald-50 px-2 py-1 rounded-lg uppercase tracking-widest flex items-center gap-1"><CheckCircle2 size={10} /> Verified</span>
                                                ) : (
                                                    <span className="text-[9px] font-black text-amber-500 bg-amber-50 px-2 py-1 rounded-lg uppercase tracking-widest flex items-center gap-1"><AlertCircle size={10} /> Unverified</span>
                                                )}
                                            </div>
                                        ))}
                                    </div>
                                </div>

                                {/* ACTIVE SESSIONS */}
                                <div className="space-y-4 pt-4 border-t border-slate-100">
                                    <div className="flex justify-between items-center">
                                        <h4 className="text-sm font-black text-slate-900">Active Sessions</h4>
                                        {activeSessions.length > 0 && (
                                            <button onClick={handleRevokeOtherSessions} disabled={executing} className="text-[10px] font-bold text-rose-600 uppercase hover:bg-rose-50 px-3 py-1.5 rounded-lg transition-all flex items-center gap-1"><Ban size={12} /> Derrubar todas as sessões ativas</button>
                                        )}
                                    </div>
                                    {loadingSessions ? (
                                        <div className="flex justify-center py-4"><Loader2 className="animate-spin text-indigo-400" size={20} /></div>
                                    ) : activeSessions.length === 0 ? (
                                        <p className="text-xs text-slate-400 italic">No active sessions found.</p>
                                    ) : (
                                        <div className="space-y-2">
                                            {activeSessions.map((s: any) => (
                                                <div key={s.id} className="flex items-center justify-between bg-slate-50 p-4 rounded-2xl border border-slate-100 gap-4">
                                                    <div className="flex items-start gap-3 overflow-hidden">
                                                        <div className="w-8 h-8 shrink-0 bg-white rounded-lg flex items-center justify-center shadow-sm text-indigo-600 mt-0.5">
                                                            <Server size={14} />
                                                        </div>
                                                        <div className="overflow-hidden">
                                                            <p className="text-xs font-bold text-slate-700 truncate min-w-[100px]" title={s.user_agent}>{s.user_agent || 'Unknown Device'}</p>
                                                            <div className="flex items-center gap-2 mt-1">
                                                                <span className="text-[10px] bg-slate-200 text-slate-600 px-2 py-0.5 rounded font-mono truncate">{s.ip_address || 'IP Unknown'}</span>
                                                                <span className="text-[9px] text-slate-400 font-bold uppercase shrink-0">Created: {new Date(s.created_at).toLocaleDateString()}</span>
                                                            </div>
                                                        </div>
                                                    </div>
                                                    <button onClick={() => handleRevokeSession(s.id)} disabled={executing} className="shrink-0 p-2 text-rose-400 hover:text-rose-600 hover:bg-white rounded-lg transition-all" title="Revoke Device">
                                                        <X size={16} />
                                                    </button>
                                                </div>
                                            ))}
                                        </div>
                                    )}
                                </div>

                                {/* ACTIONS */}
                                <div className="pt-6 border-t border-slate-100 flex gap-4">
                                    <button onClick={() => handleBlockUser(selectedUser)} className={`flex-1 py-4 rounded-2xl text-xs font-black uppercase tracking-widest transition-all ${selectedUser.banned ? 'bg-emerald-600 text-white hover:bg-emerald-700' : 'bg-amber-100 text-amber-700 hover:bg-amber-200'}`}>
                                        {selectedUser.banned ? 'Unban User' : 'Ban User'}
                                    </button>
                                    <button onClick={() => { setPanicForm({ target_type: 'user', target_value: selectedUser.id, reason: 'Sovereign Panic Signal from Identity Map' }); setShowPanicModal(true); }} className="flex-1 py-4 bg-slate-900 text-white rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-rose-600 transition-all">
                                        Sovereign Panic Signal
                                    </button>
                                    <button onClick={() => { setShowDeleteModal({ id: selectedUser.id }); }} className="flex-1 py-4 bg-rose-50 text-rose-600 rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-rose-600 hover:text-white transition-all">
                                        Delete User
                                    </button>
                                </div>
                            </div>
                        </div>
                    </div>
                )
            }

            {/* Verify Password Modal (For Revealing Sensitive Data) */}
            {
                showVerifyModal && (
                    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[800] flex items-center justify-center p-8 animate-in zoom-in-95">
                        <div className="bg-white rounded-[3rem] p-10 max-w-sm w-full shadow-2xl text-center border border-slate-200">
                            <Lock size={40} className="mx-auto text-slate-900 mb-6" />
                            <h3 className="text-xl font-black text-slate-900 mb-2">Confirmação de Segurança</h3>
                            <p className="text-xs text-slate-500 font-bold mb-8">Digite sua senha administrativa para revelar os dados sensíveis dos usuários.</p>
                            <form onSubmit={(e) => { e.preventDefault(); handleVerifyPassword(); }}>
                                <input
                                    type="password"
                                    autoFocus
                                    value={verifyPassword}
                                    onChange={e => setVerifyPassword(e.target.value)}
                                    className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-4 px-6 text-center font-bold text-slate-900 outline-none mb-6 focus:ring-4 focus:ring-indigo-500/10"
                                    placeholder="••••••••"
                                />
                                <button type="submit" disabled={executing} className="w-full bg-slate-900 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl hover:bg-indigo-600 transition-all flex items-center justify-center gap-2">
                                    {executing ? <Loader2 className="animate-spin" size={16} /> : 'Confirmar Acesso'}
                                </button>
                            </form>
                            <button onClick={() => { setShowVerifyModal(false); setVerifyPassword(''); }} className="mt-4 text-xs font-bold text-slate-400 hover:text-slate-600">Cancelar</button>
                        </div>
                    </div>
                )
            }

            {/* APP CLIENT CREATION MODAL */}
            {
                showAppClientModal && (
                    <div className="fixed inset-0 bg-slate-900/40 backdrop-blur-sm z-[1000] flex justify-center items-center p-4">
                        <div className="bg-white max-w-lg w-full rounded-[2.5rem] shadow-2xl overflow-hidden animate-in zoom-in-95 duration-200">
                            <div className="p-10 border-b border-slate-100">
                                <h2 className="text-2xl font-black text-slate-900">Create App Client</h2>
                                <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-2">Generate a scoped Identity-Aware Key</p>
                            </div>
                            <div className="p-10 space-y-6 bg-slate-50/50">
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Client Name</label>
                                    <input
                                        autoFocus
                                        value={newAppClientConfig.name}
                                        onChange={e => setNewAppClientConfig({ ...newAppClientConfig, name: e.target.value })}
                                        placeholder="e.g. Driver Mobile App"
                                        className="w-full bg-white border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none font-mono"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Specific Site URL (Redirect)</label>
                                    <input
                                        value={newAppClientConfig.site_url}
                                        onChange={e => setNewAppClientConfig({ ...newAppClientConfig, site_url: e.target.value })}
                                        placeholder="exp://driver.app"
                                        className="w-full bg-white border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none font-mono"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Allowed Origins (CORS)</label>
                                    <input
                                        value={newAppClientConfig.allowed_origins}
                                        onChange={e => setNewAppClientConfig({ ...newAppClientConfig, allowed_origins: e.target.value })}
                                        placeholder="https://driver.com, exp://driver.app"
                                        className="w-full bg-white border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none font-mono"
                                    />
                                </div>
                                {/* Table Access Control - Compact Grid UI */}
                                <div className="space-y-3 border-t border-slate-200 pt-4">
                                    {/* Schema Selector */}
                                    <div className="flex items-center justify-between">
                                        <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest">Schema</label>
                                        <select 
                                            value={selectedSchema}
                                            onChange={(e) => setSelectedSchema(e.target.value)}
                                            className="text-xs font-bold bg-white border border-slate-200 rounded-xl py-1.5 px-3 outline-none focus:ring-2 focus:ring-indigo-500"
                                        >
                                            {(availableSchemas || []).map((schema: string) => (
                                                <option key={schema} value={schema}>{schema}</option>
                                            ))}
                                        </select>
                                    </div>
                                    
                                    {/* Tables Grid with Check/Block Icons */}
                                    <div>
                                        <div className="flex items-center justify-between mb-2">
                                            <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Tables</label>
                                            <div className="flex items-center gap-3 text-[9px]">
                                                <span className="flex items-center gap-1 text-emerald-600">
                                                    <CheckCircle2 size={12} /> Allow
                                                </span>
                                                <span className="flex items-center gap-1 text-rose-500">
                                                    <Ban size={12} /> Block
                                                </span>
                                            </div>
                                        </div>
                                        
                                        <div className="max-h-40 overflow-y-auto bg-white border border-slate-200 rounded-2xl p-3">
                                            {((tablesBySchema[selectedSchema] || [])).length === 0 ? (
                                                <p className="text-xs text-slate-400 text-center py-4">No tables in this schema</p>
                                            ) : (
                                                <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2">
                                                    {(tablesBySchema[selectedSchema] || []).sort().map((table: string) => {
                                                        const fullTableName = selectedSchema === 'public' ? table : `${selectedSchema}.${table}`;
                                                        const isAllowed = (newAppClientConfig.allowed_tables || []).includes(fullTableName);
                                                        const isBlocked = (newAppClientConfig.blocked_tables || []).includes(fullTableName);
                                                        
                                                        // Logic: If not in allowed list and not public schema, it's blocked by default
                                                        const isDefaultBlocked = selectedSchema !== 'public' && !isAllowed && !isBlocked;
                                                        
                                                        return (
                                                            <div key={table} className="flex items-center justify-between bg-slate-50 hover:bg-slate-100 rounded-lg px-2 py-1.5 group">
                                                                <span className="text-[10px] font-mono text-slate-700 truncate pr-1" title={fullTableName}>
                                                                    {table}
                                                                </span>
                                                                <div className="flex items-center gap-1">
                                                                    {/* Allow Button */}
                                                                    <button
                                                                        onClick={() => {
                                                                            const currentAllowed = newAppClientConfig.allowed_tables || [];
                                                                            const currentBlocked = newAppClientConfig.blocked_tables || [];
                                                                            let newAllowed: string[];
                                                                            let newBlocked: string[];
                                                                            
                                                                            if (isAllowed) {
                                                                                // Remove from allowed
                                                                                newAllowed = currentAllowed.filter((t: string) => t !== fullTableName);
                                                                                newBlocked = currentBlocked;
                                                                            } else {
                                                                                // Add to allowed, remove from blocked if there
                                                                                newAllowed = [...currentAllowed, fullTableName];
                                                                                newBlocked = currentBlocked.filter((t: string) => t !== fullTableName);
                                                                            }
                                                                            setNewAppClientConfig({ 
                                                                                ...newAppClientConfig, 
                                                                                allowed_tables: newAllowed,
                                                                                blocked_tables: newBlocked
                                                                            });
                                                                        }}
                                                                        className={`p-1 rounded transition-colors ${
                                                                            isAllowed 
                                                                                ? 'bg-emerald-100 text-emerald-600' 
                                                                                : 'text-slate-300 hover:text-emerald-500'
                                                                        }`}
                                                                        title="Allow access"
                                                                    >
                                                                        <CheckCircle2 size={14} />
                                                                    </button>
                                                                    {/* Block Button */}
                                                                    <button
                                                                        onClick={() => {
                                                                            const currentAllowed = newAppClientConfig.allowed_tables || [];
                                                                            const currentBlocked = newAppClientConfig.blocked_tables || [];
                                                                            let newAllowed: string[];
                                                                            let newBlocked: string[];
                                                                            
                                                                            if (isBlocked || isDefaultBlocked) {
                                                                                // Remove from blocked
                                                                                newBlocked = currentBlocked.filter((t: string) => t !== fullTableName);
                                                                                newAllowed = currentAllowed;
                                                                            } else {
                                                                                // Add to blocked, remove from allowed if there
                                                                                newBlocked = [...currentBlocked, fullTableName];
                                                                                newAllowed = currentAllowed.filter((t: string) => t !== fullTableName);
                                                                            }
                                                                            setNewAppClientConfig({ 
                                                                                ...newAppClientConfig, 
                                                                                allowed_tables: newAllowed,
                                                                                blocked_tables: newBlocked
                                                                            });
                                                                        }}
                                                                        className={`p-1 rounded transition-colors ${
                                                                            isBlocked || isDefaultBlocked
                                                                                ? 'bg-rose-100 text-rose-500' 
                                                                                : 'text-slate-300 hover:text-rose-500'
                                                                        }`}
                                                                        title="Block access"
                                                                    >
                                                                        <Ban size={14} />
                                                                    </button>
                                                                </div>
                                                            </div>
                                                        );
                                                    })}
                                                </div>
                                            )}
                                        </div>
                                        
                                        {/* Bulk Actions */}
                                        <div className="flex items-center justify-end gap-2 mt-2">
                                            <button
                                                onClick={() => {
                                                    const schemaTables = (tablesBySchema[selectedSchema] || []).map((t: string) => 
                                                        selectedSchema === 'public' ? t : `${selectedSchema}.${t}`
                                                    );
                                                    const currentAllowed = newAppClientConfig.allowed_tables || [];
                                                    // Add all schema tables to allowed, remove from blocked
                                                    const newAllowed = [...new Set([...currentAllowed, ...schemaTables])];
                                                    const newBlocked = (newAppClientConfig.blocked_tables || []).filter(
                                                        (t: string) => !schemaTables.includes(t)
                                                    );
                                                    setNewAppClientConfig({
                                                        ...newAppClientConfig,
                                                        allowed_tables: newAllowed,
                                                        blocked_tables: newBlocked
                                                    });
                                                }}
                                                className="text-[9px] font-bold text-emerald-600 hover:text-emerald-700 px-2 py-1 rounded hover:bg-emerald-50 transition-colors"
                                            >
                                                Select All
                                            </button>
                                            <span className="text-slate-300">|</span>
                                            <button
                                                onClick={() => {
                                                    const schemaTables = (tablesBySchema[selectedSchema] || []).map((t: string) => 
                                                        selectedSchema === 'public' ? t : `${selectedSchema}.${t}`
                                                    );
                                                    const currentBlocked = newAppClientConfig.blocked_tables || [];
                                                    // Add all schema tables to blocked, remove from allowed
                                                    const newBlocked = [...new Set([...currentBlocked, ...schemaTables])];
                                                    const newAllowed = (newAppClientConfig.allowed_tables || []).filter(
                                                        (t: string) => !schemaTables.includes(t)
                                                    );
                                                    setNewAppClientConfig({
                                                        ...newAppClientConfig,
                                                        allowed_tables: newAllowed,
                                                        blocked_tables: newBlocked
                                                    });
                                                }}
                                                className="text-[9px] font-bold text-rose-500 hover:text-rose-600 px-2 py-1 rounded hover:bg-rose-50 transition-colors"
                                            >
                                                Block All
                                            </button>
                                        </div>
                                    </div>
                                    
                                    <p className="text-[9px] text-slate-400">
                                        <span className="font-semibold">Public schema:</span> All tables allowed by default. 
                                        <span className="font-semibold"> Other schemas:</span> Blocked by default unless explicitly allowed.
                                    </p>
                                </div>
                            </div>
                            <div className="p-6 border-t border-slate-100 flex justify-end gap-3 bg-white">
                                <button onClick={() => setShowAppClientModal(false)} className="px-6 py-3 rounded-2xl text-xs font-black text-slate-500 uppercase tracking-widest hover:bg-slate-50 transition-all">Cancel</button>
                                <button onClick={handleSaveAppClient} disabled={executing || !newAppClientConfig.name} className="bg-indigo-600 text-white px-8 py-3 rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-indigo-700 transition-all shadow-xl shadow-indigo-200 disabled:opacity-50">Create Key</button>
                            </div>
                        </div>
                    </div>
                )
            }

            {/* APP CLIENT CONFIRMATION MODAL (Update/Rotate/Delete with Password) */}
            {
                showConfirmModal && (
                    <div className="fixed inset-0 bg-slate-900/60 backdrop-blur-sm z-[1100] flex justify-center items-center p-4">
                        <div className="bg-white max-w-md w-full rounded-[2.5rem] shadow-2xl overflow-hidden animate-in zoom-in-95 duration-200">
                            <div className="p-10 border-b border-slate-100">
                                <div className="flex items-center gap-4">
                                    <div className={`w-14 h-14 rounded-2xl flex items-center justify-center shadow-lg ${
                                        confirmAction === 'rotate' ? 'bg-amber-500' : 
                                        confirmAction === 'update' ? 'bg-indigo-500' : 'bg-rose-500'
                                    }`}>
                                        {confirmAction === 'rotate' ? <RefreshCcw size={28} className="text-white" /> : 
                                         confirmAction === 'update' ? <Edit2 size={28} className="text-white" /> : 
                                         <Trash2 size={28} className="text-white" />}
                                    </div>
                                    <div>
                                        <h3 className="text-xl font-black text-slate-900">
                                            {confirmAction === 'rotate' ? 'Rotate App Key' : 
                                             confirmAction === 'update' ? 'Update App Client' : 
                                             'Delete App Client'}
                                        </h3>
                                        <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-1">
                                            {confirmAction === 'rotate' ? 'Generate new HMAC key' : 
                                             confirmAction === 'update' ? 'Update URLs and CORS settings' : 
                                             'Revoke access permanently'}
                                        </p>
                                    </div>
                                </div>
                            </div>
                            <div className="p-10 space-y-6">
                                {confirmAction === 'rotate' && rotatedKey && (
                                    <div className="bg-emerald-50 border border-emerald-200 rounded-2xl p-4">
                                        <label className="text-[10px] font-black text-emerald-600 uppercase tracking-widest">New Key Generated</label>
                                        <div className="flex items-center gap-2 mt-2">
                                            <code className="text-[10px] bg-white text-emerald-700 px-2 py-1 rounded truncate flex-1 border border-emerald-200">{rotatedKey}</code>
                                            <button onClick={() => safeCopy(rotatedKey)} className="text-emerald-600 hover:text-emerald-800"><Copy size={16} /></button>
                                        </div>
                                        <p className="text-[10px] text-emerald-600 mt-2">Store this key securely - it won&apos;t be shown again!</p>
                                    </div>
                                )}
                                
                                <div className="space-y-3">
                                    <label className="text-[10px] font-black text-slate-500 uppercase tracking-widest ml-1">
                                        Confirm Your Password
                                    </label>
                                    <input
                                        type="password"
                                        value={confirmPassword}
                                        onChange={(e) => setConfirmPassword(e.target.value)}
                                        placeholder="Enter your password to confirm..."
                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none focus:ring-2 focus:ring-indigo-500"
                                        autoFocus
                                    />
                                    <p className="text-[10px] text-slate-400 ml-1">
                                        This action requires password verification for security.
                                    </p>
                                </div>
                            </div>
                            <div className="p-6 border-t border-slate-100 flex justify-end gap-3 bg-white">
                                <button 
                                    onClick={() => { setShowConfirmModal(false); setConfirmPassword(''); setRotatedKey(null); }} 
                                    className="px-6 py-3 rounded-2xl text-xs font-black text-slate-500 uppercase tracking-widest hover:bg-slate-50 transition-all"
                                >
                                    Cancel
                                </button>
                                {confirmAction === 'update' ? (
                                    <button 
                                        onClick={executeUpdateAppClient} 
                                        disabled={executing || !confirmPassword}
                                        className="bg-indigo-500 text-white px-8 py-3 rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-indigo-600 transition-all shadow-xl shadow-indigo-200 disabled:opacity-50 flex items-center gap-2"
                                    >
                                        {executing ? <Loader2 size={16} className="animate-spin" /> : <Edit2 size={16} />}
                                        Update Client
                                    </button>
                                ) : confirmAction === 'rotate' && !rotatedKey ? (
                                    <button 
                                        onClick={executeRotateAppClient} 
                                        disabled={executing || !confirmPassword}
                                        className="bg-amber-500 text-white px-8 py-3 rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-amber-600 transition-all shadow-xl shadow-amber-200 disabled:opacity-50 flex items-center gap-2"
                                    >
                                        {executing ? <Loader2 size={16} className="animate-spin" /> : <RefreshCcw size={16} />}
                                        Rotate Key
                                    </button>
                                ) : confirmAction === 'delete' ? (
                                    <button 
                                        onClick={executeDeleteAppClient} 
                                        disabled={executing || !confirmPassword}
                                        className="bg-rose-500 text-white px-8 py-3 rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-rose-600 transition-all shadow-xl shadow-rose-200 disabled:opacity-50 flex items-center gap-2"
                                    >
                                        {executing ? <Loader2 size={16} className="animate-spin" /> : <Trash2 size={16} />}
                                        Delete Forever
                                    </button>
                                ) : (
                                    <button 
                                        onClick={() => { setShowConfirmModal(false); setConfirmPassword(''); setRotatedKey(null); }}
                                        className="bg-emerald-500 text-white px-8 py-3 rounded-2xl text-xs font-black uppercase tracking-widest hover:bg-emerald-600 transition-all shadow-xl shadow-emerald-200 flex items-center gap-2"
                                    >
                                        <CheckCircle2 size={16} />
                                        Done
                                    </button>
                                )}
                            </div>
                        </div>
                    </div>
                )
            }

            {/* CREATE TEMPLATE MODAL */}
            {
                showCreateTemplateModal && (
                    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8 animate-in zoom-in-95">
                        <div className="bg-white rounded-[3rem] w-full max-w-md p-10 shadow-2xl relative">
                            <button onClick={() => setShowCreateTemplateModal(false)} className="absolute top-8 right-8 text-slate-300 hover:text-slate-900"><X size={24} /></button>
                            <div className="flex items-center gap-3 mb-6">
                                <div className="w-12 h-12 bg-indigo-600 text-white rounded-2xl flex items-center justify-center shadow-lg"><LayoutTemplate size={24} /></div>
                                <div>
                                    <h3 className="text-xl font-black text-slate-900">New Message Template</h3>
                                    <p className="text-[10px] font-bold text-slate-400 uppercase tracking-widest mt-0.5">i18n Reusable Template</p>
                                </div>
                            </div>
                            <div className="space-y-4">
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Template Name</label>
                                    <input
                                        autoFocus
                                        value={newTemplateForm.name}
                                        onChange={(e) => setNewTemplateForm({ ...newTemplateForm, name: e.target.value })}
                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                        placeholder="e.g. OTP SMS Code"
                                    />
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Message Type</label>
                                    <select
                                        value={newTemplateForm.type}
                                        onChange={(e) => setNewTemplateForm({ ...newTemplateForm, type: e.target.value })}
                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none"
                                    >
                                        <option value="otp_challenge">OTP Challenge</option>
                                        <option value="confirmation">Confirmation</option>
                                        <option value="recovery">Recovery</option>
                                        <option value="magic_link">Magic Link</option>
                                        <option value="login_alert">Login Alert</option>
                                        <option value="welcome">Welcome</option>
                                    </select>
                                </div>
                                <div className="space-y-2">
                                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Default Language (ISO Code)</label>
                                    <input
                                        value={newTemplateForm.default_language}
                                        onChange={(e) => setNewTemplateForm({ ...newTemplateForm, default_language: e.target.value })}
                                        className="w-full bg-slate-50 border border-slate-200 rounded-2xl py-3 px-4 text-sm font-bold outline-none font-mono"
                                        placeholder="en-US"
                                    />
                                    <p className="text-[9px] text-slate-400 px-1">ISO 639 code. Examples: en-US, pt-BR, es-ES, fr-FR, de-DE, ja-JP</p>
                                </div>
                                <button
                                    onClick={handleCreateTemplate}
                                    disabled={!newTemplateForm.name}
                                    className="w-full bg-indigo-600 text-white py-4 rounded-2xl font-black text-xs uppercase tracking-widest shadow-xl mt-2 hover:bg-indigo-700 transition-all disabled:opacity-50"
                                >
                                    Create Template
                                </button>
                            </div>
                        </div>
                    </div>
                )
            }
        </div >
    );
};

export default AuthConfig;
