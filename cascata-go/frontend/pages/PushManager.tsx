import React, { useState, useEffect, useCallback } from 'react';
import { 
  Bell, Smartphone, Send, Plus, Trash2, 
  Loader2, CheckCircle2, AlertCircle, RefreshCw, 
  User, Clock, Zap, History,
  Check, XCircle, Globe, Users, Target, Settings, FileText,
  Smartphone as SmartphoneIcon, Laptop, Megaphone
} from 'lucide-react';

const PushManager: React.FC<{ projectId: string }> = ({ projectId }) => {
  const [activeTab, setActiveTab] = useState<'campaigns' | 'templates' | 'groups' | 'devices' | 'rules' | 'config'>('campaigns');
  const [loading, setLoading] = useState(true);
  const [success, setSuccess] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  
  const [devices, setDevices] = useState<any[]>([]);
  const [templates, setTemplates] = useState<any[]>([]);
  const [groups, setGroups] = useState<any[]>([]);
  const [campaigns, setCampaigns] = useState<any[]>([]);
  const [rules, setRules] = useState<any[]>([]);
  const [history, setHistory] = useState<any[]>([]);
  const [fcmConfig, setFcmConfig] = useState<any>(null);
  const [stats, setStats] = useState<any>(null);

  // Modal states
  const [showCampaignModal, setShowCampaignModal] = useState(false);
  const [showTemplateModal, setShowTemplateModal] = useState(false);
  const [showGroupModal, setShowGroupModal] = useState(false);
  const [showRuleModal, setShowRuleModal] = useState(false);
  const [showFCMModal, setShowFCMModal] = useState(false);
  const [editingGroup, setEditingGroup] = useState<any>(null);

  // Form states
  const [campaignForm, setCampaignForm] = useState({ name: '', title: '', body: '', target_type: 'user', target_user_id: '', target_group_id: '', template_id: '', language: 'pt' });
  const [templateForm, setTemplateForm] = useState({ code: '', name: '', description: '', default_language: 'pt', active: true, content_i18n: { pt: { title: '', body: '' } } });
  const [groupForm, setGroupForm] = useState({ name: '', description: '', filter_config: { table: 'users', conditions: [] } });
  const [ruleForm, setRuleForm] = useState({ name: '', trigger_table: '', trigger_event: 'INSERT', recipient_column: 'user_id', title_template: '', body_template: '' });
  const [fcmForm, setFcmForm] = useState({ project_id: '', client_email: '', private_key: '' });

  // ESC key handler for modals
  useEffect(() => {
    const handleEsc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setShowCampaignModal(false);
        setShowTemplateModal(false);
        setShowGroupModal(false);
        setShowRuleModal(false);
        setShowFCMModal(false);
        setEditingGroup(null);
      }
    };
    window.addEventListener('keydown', handleEsc);
    return () => window.removeEventListener('keydown', handleEsc);
  }, []);

  const showNotification = (msg: string, isError = false) => {
    if (isError) setError(msg);
    else setSuccess(msg);
    setTimeout(() => { setError(null); setSuccess(null); }, 3000);
  };

  const fetchData = async () => {
    setLoading(true);
    try {
      const token = localStorage.getItem('cascata_token');
      const headers = { 'Authorization': `Bearer ${token}` };

      const [devicesRes, templatesRes, groupsRes, campaignsRes, rulesRes, historyRes, fcmRes, statsRes] = await Promise.all([
        fetch(`/api/data/${projectId}/push/devices`, { headers }),
        fetch(`/api/data/${projectId}/push/templates`, { headers }),
        fetch(`/api/data/${projectId}/push/groups`, { headers }),
        fetch(`/api/data/${projectId}/push/campaigns`, { headers }),
        fetch(`/api/data/${projectId}/push/rules`, { headers }),
        fetch(`/api/data/${projectId}/push/history`, { headers }),
        fetch(`/api/data/${projectId}/push/config`, { headers }),
        fetch(`/api/data/${projectId}/push/stats`, { headers })
      ]);

      if (devicesRes.ok) {
        const devicesData = await devicesRes.json();
        setDevices(Array.isArray(devicesData) ? devicesData : []);
      } else {
        setDevices([]);
      }
      if (templatesRes.ok) {
        const templatesData = await templatesRes.json();
        setTemplates(Array.isArray(templatesData) ? templatesData : []);
      } else {
        setTemplates([]);
      }
      if (groupsRes.ok) {
        const groupsData = await groupsRes.json();
        setGroups(Array.isArray(groupsData) ? groupsData : []);
      } else {
        setGroups([]);
      }
      if (campaignsRes.ok) {
        const campaignsData = await campaignsRes.json();
        setCampaigns(Array.isArray(campaignsData) ? campaignsData : []);
      } else {
        setCampaigns([]);
      }
      if (rulesRes.ok) {
        const rulesData = await rulesRes.json();
        setRules(Array.isArray(rulesData) ? rulesData : []);
      } else {
        setRules([]);
      }
      if (historyRes.ok) {
        const historyData = await historyRes.json();
        setHistory(Array.isArray(historyData) ? historyData : []);
      } else {
        setHistory([]);
      }
      if (fcmRes.ok) {
        const fcmData = await fcmRes.json();
        setFcmConfig(fcmData || null);
      } else {
        setFcmConfig(null);
      }
      if (statsRes.ok) {
        setStats(await statsRes.json());
      }
    } catch (e) {
      showNotification('Falha ao carregar dados', true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchData(); }, [projectId]);

  // API Functions
  const saveFCM = async () => {
    try {
      const token = localStorage.getItem('cascata_token');
      const res = await fetch(`/api/data/${projectId}/push/config`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify(fcmForm)
      });
      if (!res.ok) throw new Error('Failed');
      setFcmConfig({ project_id: fcmForm.project_id, client_email: fcmForm.client_email });
      setShowFCMModal(false);
      showNotification('FCM configurado!');
    } catch (e: any) {
      showNotification(e.message, true);
    }
  };

  const createCampaign = async () => {
    try {
      const token = localStorage.getItem('cascata_token');
      const res = await fetch(`/api/data/${projectId}/push/campaigns`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify(campaignForm)
      });
      if (!res.ok) throw new Error('Failed');
      setShowCampaignModal(false);
      showNotification('Campanha criada!');
      fetchData();
    } catch (e: any) {
      showNotification(e.message, true);
    }
  };

  const createTemplate = async () => {
    try {
      const token = localStorage.getItem('cascata_token');
      const res = await fetch(`/api/data/${projectId}/push/templates`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify(templateForm)
      });
      if (!res.ok) throw new Error('Failed');
      setShowTemplateModal(false);
      showNotification('Template criado!');
      fetchData();
    } catch (e: any) {
      showNotification(e.message, true);
    }
  };

  const createGroup = async () => {
    try {
      const token = localStorage.getItem('cascata_token');
      const res = await fetch(`/api/data/${projectId}/push/groups`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify(groupForm)
      });
      if (!res.ok) throw new Error('Failed');
      setShowGroupModal(false);
      showNotification('Grupo criado!');
      fetchData();
    } catch (e: any) {
      showNotification(e.message, true);
    }
  };

  const syncGroup = async (id: string) => {
    try {
      const token = localStorage.getItem('cascata_token');
      await fetch(`/api/data/${projectId}/push/groups/${id}/sync`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}` }
      });
      showNotification('Sincronizado!');
      fetchData();
    } catch (e) {
      showNotification('Erro ao sincronizar', true);
    }
  };

  const createRule = async () => {
    try {
      const token = localStorage.getItem('cascata_token');
      const res = await fetch(`/api/data/${projectId}/push/rules`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify(ruleForm)
      });
      if (!res.ok) throw new Error('Failed');
      setShowRuleModal(false);
      showNotification('Regra criada!');
      fetchData();
    } catch (e: any) {
      showNotification(e.message, true);
    }
  };

  const deleteRule = async (id: string) => {
    if (!confirm('Deletar?')) return;
    try {
      const token = localStorage.getItem('cascata_token');
      await fetch(`/api/data/${projectId}/push/rules/${id}`, {
        method: 'DELETE',
        headers: { 'Authorization': `Bearer ${token}` }
      });
      showNotification('Deletado!');
      fetchData();
    } catch (e) {
      showNotification('Erro', true);
    }
  };

  const cancelCampaign = async (id: string) => {
    try {
      const token = localStorage.getItem('cascata_token');
      await fetch(`/api/data/${projectId}/push/campaigns/${id}/cancel`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${token}` }
      });
      showNotification('Cancelado!');
      fetchData();
    } catch (e) {
      showNotification('Erro', true);
    }
  };

  const getStatusBadge = (status: string) => {
    const colors: Record<string, string> = {
      pending: 'bg-amber-50 text-amber-600',
      scheduled: 'bg-blue-50 text-blue-600',
      sending: 'bg-indigo-50 text-indigo-600',
      completed: 'bg-emerald-50 text-emerald-600',
      failed: 'bg-rose-50 text-rose-600',
      cancelled: 'bg-slate-50 text-slate-400'
    };
    return <span className={`px-2 py-1 rounded text-[9px] font-black uppercase ${colors[status] || colors.pending}`}>{status}</span>;
  };

  if (loading) return <div className="p-20 flex justify-center"><Loader2 className="animate-spin text-indigo-600" size={32} /></div>;

  return (
    <div className="p-8 lg:p-12 max-w-7xl mx-auto w-full space-y-8 pb-40">
      {/* Notifications */}
      {(success || error) && (
        <div className={`fixed top-8 left-1/2 -translate-x-1/2 z-[600] px-6 py-4 rounded-full shadow-2xl flex items-center gap-3 ${error ? 'bg-rose-600 text-white' : 'bg-emerald-600 text-white'}`}>
          {error ? <AlertCircle size={18} /> : <CheckCircle2 size={18} />}
          <span className="text-xs font-bold">{success || error}</span>
        </div>
      )}

      {/* Header */}
      <header className="flex flex-col lg:flex-row lg:items-end justify-between gap-8">
        <div className="flex items-center gap-6">
          <div className="w-16 h-16 bg-gradient-to-br from-amber-400 to-orange-500 text-white rounded-2xl flex items-center justify-center shadow-xl">
            <Bell size={32} />
          </div>
          <div>
            <h2 className="text-4xl font-black text-slate-900 tracking-tighter">Push Engine</h2>
            <p className="text-slate-500 font-medium mt-1">
              {fcmConfig ? (
                <span className="flex items-center gap-2">
                  <CheckCircle2 size={14} className="text-emerald-500" />
                  FCM Ativo: {fcmConfig.project_id}
                </span>
              ) : (
                <span className="flex items-center gap-2 text-amber-600">
                  <AlertCircle size={14} />
                  Configure FCM para ativar push
                </span>
              )}
            </p>
          </div>
        </div>
        
        <button 
          onClick={() => setShowFCMModal(true)}
          className={`px-6 py-3 rounded-xl font-black text-xs uppercase tracking-widest flex items-center gap-2 transition-all ${fcmConfig ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700'}`}
        >
          <Settings size={16} />
          {fcmConfig ? 'FCM Configurado' : 'Configurar FCM'}
        </button>
      </header>

      {/* Stats */}
      {stats && (
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <div className="bg-white border border-slate-200 rounded-2xl p-6 shadow-sm">
            <div className="flex items-center gap-3 mb-2">
              <div className="w-10 h-10 bg-emerald-50 rounded-xl flex items-center justify-center"><Send size={18} className="text-emerald-600" /></div>
              <span className="text-[10px] font-black text-slate-400 uppercase">Enviados</span>
            </div>
            <p className="text-2xl font-black text-slate-900">{stats.total_sent || 0}</p>
            <p className="text-[10px] text-slate-400">Hoje: {stats.today_sent || 0}</p>
          </div>
          <div className="bg-white border border-slate-200 rounded-2xl p-6 shadow-sm">
            <div className="flex items-center gap-3 mb-2">
              <div className="w-10 h-10 bg-blue-50 rounded-xl flex items-center justify-center"><Smartphone size={18} className="text-blue-600" /></div>
              <span className="text-[10px] font-black text-slate-400 uppercase">Dispositivos</span>
            </div>
            <p className="text-2xl font-black text-slate-900">{devices.length}</p>
          </div>
          <div className="bg-white border border-slate-200 rounded-2xl p-6 shadow-sm">
            <div className="flex items-center gap-3 mb-2">
              <div className="w-10 h-10 bg-violet-50 rounded-xl flex items-center justify-center"><Users size={18} className="text-violet-600" /></div>
              <span className="text-[10px] font-black text-slate-400 uppercase">Grupos</span>
            </div>
            <p className="text-2xl font-black text-slate-900">{groups.length}</p>
          </div>
          <div className="bg-white border border-slate-200 rounded-2xl p-6 shadow-sm">
            <div className="flex items-center gap-3 mb-2">
              <div className="w-10 h-10 bg-amber-50 rounded-xl flex items-center justify-center"><FileText size={18} className="text-amber-600" /></div>
              <span className="text-[10px] font-black text-slate-400 uppercase">Templates</span>
            </div>
            <p className="text-2xl font-black text-slate-900">{templates.length}</p>
          </div>
        </div>
      )}

      {/* Tabs */}
      <div className="flex flex-wrap gap-2 bg-slate-100 p-2 rounded-2xl">
        {[
          { id: 'campaigns', label: 'Campanhas', icon: Megaphone },
          { id: 'templates', label: 'Templates I18N', icon: Globe },
          { id: 'groups', label: 'Grupos', icon: Target },
          { id: 'devices', label: 'Dispositivos', icon: Smartphone },
          { id: 'rules', label: 'Automações', icon: Zap },
          { id: 'config', label: 'Configuração', icon: Settings },
        ].map(tab => (
          <button 
            key={tab.id}
            onClick={() => setActiveTab(tab.id as any)}
            className={`px-5 py-2.5 rounded-xl text-xs font-black uppercase tracking-widest transition-all flex items-center gap-2 ${activeTab === tab.id ? 'bg-white shadow-md text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}
          >
            <tab.icon size={14} /> {tab.label}
          </button>
        ))}
      </div>

      {/* CAMPAIGNS */}
      {activeTab === 'campaigns' && (
        <div className="space-y-6">
          <div className="flex justify-between items-center">
            <h3 className="text-xl font-black text-slate-900 flex items-center gap-3"><Megaphone size={24} className="text-amber-500" /> Campanhas</h3>
            <button onClick={() => setShowCampaignModal(true)} className="bg-indigo-600 text-white px-6 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-700 shadow-lg flex items-center gap-2">
              <Plus size={16} /> Nova Campanha
            </button>
          </div>

          <div className="bg-white border border-slate-200 rounded-[2rem] shadow-sm overflow-hidden">
            <table className="w-full text-left">
              <thead>
                <tr className="bg-slate-50/50 border-b border-slate-100">
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Campanha</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Status</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Destinatários</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Progresso</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase text-right">Ações</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {campaigns.map(c => (
                  <tr key={c.id} className="hover:bg-slate-50">
                    <td className="px-6 py-4">
                      <p className="font-bold text-slate-900">{c.name}</p>
                      <p className="text-[10px] text-slate-400 uppercase">{c.target_type}</p>
                    </td>
                    <td className="px-6 py-4">{getStatusBadge(c.status)}</td>
                    <td className="px-6 py-4 text-sm font-bold">{c.total_recipients}</td>
                    <td className="px-6 py-4">
                      {c.status === 'sending' || c.status === 'completed' ? (
                        <div className="w-full max-w-[100px]">
                          <div className="h-2 bg-slate-100 rounded-full overflow-hidden">
                            <div className="h-full bg-emerald-500" style={{ width: `${c.total_recipients > 0 ? (c.sent_count / c.total_recipients) * 100 : 0}%` }} />
                          </div>
                          {c.failed_count > 0 && <p className="text-[9px] text-rose-500 mt-1">{c.failed_count} falhas</p>}
                        </div>
                      ) : <span className="text-xs text-slate-400">-</span>}
                    </td>
                    <td className="px-6 py-4 text-right">
                      {(c.status === 'pending' || c.status === 'scheduled') && (
                        <button onClick={() => cancelCampaign(c.id)} className="text-rose-500 hover:text-rose-700 text-xs font-bold">Cancelar</button>
                      )}
                    </td>
                  </tr>
                ))}
                {campaigns.length === 0 && <tr><td colSpan={5} className="py-20 text-center text-slate-400 text-xs font-bold uppercase">Nenhuma campanha</td></tr>}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* TEMPLATES */}
      {activeTab === 'templates' && (
        <div className="space-y-6">
          <div className="flex justify-between items-center">
            <h3 className="text-xl font-black text-slate-900 flex items-center gap-3"><Globe size={24} className="text-blue-500" /> Templates</h3>
            <button onClick={() => setShowTemplateModal(true)} className="bg-indigo-600 text-white px-6 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-700 shadow-lg flex items-center gap-2">
              <Plus size={16} /> Novo Template
            </button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {templates.map(t => (
              <div key={t.id} className="bg-white border border-slate-200 rounded-[2rem] p-6 shadow-sm">
                <div className="flex justify-between items-start mb-4">
                  <div className="w-12 h-12 bg-blue-50 rounded-2xl flex items-center justify-center"><FileText size={24} className="text-blue-600" /></div>
                  <span className={`px-2 py-1 rounded-lg text-[9px] font-black uppercase ${t.active ? 'bg-emerald-50 text-emerald-600' : 'bg-slate-100 text-slate-400'}`}>{t.active ? 'Ativo' : 'Inativo'}</span>
                </div>
                <h4 className="font-black text-lg text-slate-900 mb-1">{t.name}</h4>
                <p className="text-[10px] font-mono text-slate-400 mb-4">{t.code}</p>
                <div className="flex flex-wrap gap-2 mb-4">
                  {Object.keys(t.content_i18n || {}).map(lang => (
                    <span key={lang} className={`px-2 py-1 rounded-lg text-[9px] font-bold uppercase ${lang === t.default_language ? 'bg-indigo-100 text-indigo-600' : 'bg-slate-100 text-slate-600'}`}>{lang}</span>
                  ))}
                </div>
                <div className="p-3 bg-slate-50 rounded-xl">
                  <p className="text-[10px] font-bold text-slate-700 truncate">{t.content_i18n?.[t.default_language]?.title || 'Sem título'}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* GROUPS */}
      {activeTab === 'groups' && (
        <div className="space-y-6">
          <div className="flex justify-between items-center">
            <h3 className="text-xl font-black text-slate-900 flex items-center gap-3"><Target size={24} className="text-violet-500" /> Grupos</h3>
            <button onClick={() => setShowGroupModal(true)} className="bg-indigo-600 text-white px-6 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-700 shadow-lg flex items-center gap-2">
              <Plus size={16} /> Novo Grupo
            </button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {groups.map(g => (
              <div key={g.id} className="bg-white border border-slate-200 rounded-[2rem] p-6 shadow-sm">
                <div className="flex justify-between items-start mb-4">
                  <div className="w-12 h-12 bg-violet-50 rounded-2xl flex items-center justify-center"><Users size={24} className="text-violet-600" /></div>
                  <button onClick={() => { setEditingGroup(g); setGroupForm({ name: g.name, description: g.description || '', filter_config: g.filter_config || { table: 'users', conditions: [] } }); setShowGroupModal(true); }} className="p-2 hover:bg-slate-100 rounded-xl" title="Editar"><Settings size={16} className="text-slate-400" /></button>
                  <button onClick={() => syncGroup(g.id)} className="p-2 hover:bg-slate-100 rounded-xl"><RefreshCw size={16} className="text-slate-400" /></button>
                </div>
                <h4 className="font-black text-lg text-slate-900 mb-1">{g.name}</h4>
                {g.description && <p className="text-xs text-slate-500 mb-4">{g.description}</p>}
                <div className="flex items-center gap-4">
                  <div className="text-center">
                    <p className="text-2xl font-black text-violet-600">{g.user_count}</p>
                    <p className="text-[9px] font-bold text-slate-400 uppercase">Usuários</p>
                  </div>
                  {g.last_sync_at && <p className="text-xs text-slate-400">Sync: {new Date(g.last_sync_at).toLocaleDateString('pt-BR')}</p>}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* DEVICES */}
      {activeTab === 'devices' && (
        <div className="space-y-6">
          <div className="flex justify-between items-center">
            <h3 className="text-xl font-black text-slate-900 flex items-center gap-3"><Smartphone size={24} className="text-emerald-500" /> Dispositivos</h3>
            <div className="flex gap-2">
              <span className="px-4 py-2 bg-emerald-50 text-emerald-700 rounded-xl text-xs font-bold">{devices.filter(d => d.is_active).length} Ativos</span>
              <span className="px-4 py-2 bg-slate-100 text-slate-600 rounded-xl text-xs font-bold">{devices.length} Total</span>
            </div>
          </div>

          <div className="bg-white border border-slate-200 rounded-[2rem] shadow-sm overflow-hidden">
            <table className="w-full text-left">
              <thead>
                <tr className="bg-slate-50/50 border-b border-slate-100">
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Plataforma</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">User ID</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Token</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Status</th>
                  <th className="px-6 py-4 text-[10px] font-black text-slate-400 uppercase">Última Atividade</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {devices.map(d => (
                  <tr key={d.id} className="hover:bg-slate-50">
                    <td className="px-6 py-4"><div className="flex items-center gap-3"><SmartphoneIcon size={16} className={d.platform === 'android' ? 'text-emerald-500' : d.platform === 'ios' ? 'text-blue-500' : 'text-purple-500'} /><span className="text-xs font-bold capitalize">{d.platform}</span></div></td>
                    <td className="px-6 py-4"><code className="text-xs font-mono bg-slate-100 px-2 py-1 rounded">{d.user_id?.substring(0, 8)}...</code></td>
                    <td className="px-6 py-4"><code className="text-xs font-mono text-slate-400">{d.token?.substring(0, 20)}...</code></td>
                    <td className="px-6 py-4">{d.is_active ? <span className="px-2 py-1 bg-emerald-50 text-emerald-600 rounded text-[9px] font-black uppercase">Ativo</span> : <span className="px-2 py-1 bg-slate-100 text-slate-400 rounded text-[9px] font-black uppercase">Inativo</span>}</td>
                    <td className="px-6 py-4 text-xs text-slate-500">{new Date(d.last_active_at).toLocaleString('pt-BR')}</td>
                  </tr>
                ))}
                {devices.length === 0 && <tr><td colSpan={5} className="py-20 text-center text-slate-400 text-xs font-bold uppercase">Nenhum dispositivo</td></tr>}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* RULES */}
      {activeTab === 'rules' && (
        <div className="space-y-6">
          <div className="flex justify-between items-center">
            <h3 className="text-xl font-black text-slate-900 flex items-center gap-3"><Zap size={24} className="text-amber-500" /> Automações</h3>
            <button onClick={() => setShowRuleModal(true)} className="bg-indigo-600 text-white px-6 py-3 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-700 shadow-lg flex items-center gap-2">
              <Plus size={16} /> Nova Regra
            </button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            {rules.map(r => (
              <div key={r.id} className="bg-white border border-slate-200 rounded-[2rem] p-6 shadow-sm">
                <div className="flex justify-between items-start mb-4">
                  <div className="w-12 h-12 bg-amber-50 rounded-2xl flex items-center justify-center"><Zap size={24} className="text-amber-600" /></div>
                  <button onClick={() => deleteRule(r.id)} className="text-slate-300 hover:text-rose-600"><Trash2 size={18} /></button>
                </div>
                <h4 className="font-black text-lg text-slate-900 mb-1">{r.name}</h4>
                <p className="text-[10px] font-bold text-slate-400 uppercase mb-4">ON {r.trigger_event} {r.trigger_table}</p>
                <div className="p-4 bg-slate-50 rounded-2xl space-y-2">
                  <p className="text-xs font-bold text-slate-700">{r.title_template}</p>
                  <p className="text-xs text-slate-500">{r.body_template}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* CONFIG */}
      {activeTab === 'config' && (
        <div className="space-y-6">
          <h3 className="text-xl font-black text-slate-900 flex items-center gap-3"><Settings size={24} className="text-slate-500" /> Configuração FCM</h3>
          
          <div className="bg-white border border-slate-200 rounded-[2rem] p-8 shadow-sm">
            {fcmConfig ? (
              <div className="space-y-6">
                <div className="flex items-center gap-4">
                  <div className="w-16 h-16 bg-emerald-50 rounded-2xl flex items-center justify-center"><CheckCircle2 size={32} className="text-emerald-600" /></div>
                  <div>
                    <h4 className="font-black text-lg text-slate-900">FCM Configurado</h4>
                    <p className="text-sm text-slate-500">Firebase Cloud Messaging ativo</p>
                  </div>
                </div>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <div className="p-4 bg-slate-50 rounded-xl">
                    <p className="text-[10px] font-black text-slate-400 uppercase mb-1">Project ID</p>
                    <p className="text-sm font-mono text-slate-700">{fcmConfig.project_id}</p>
                  </div>
                  <div className="p-4 bg-slate-50 rounded-xl">
                    <p className="text-[10px] font-black text-slate-400 uppercase mb-1">Client Email</p>
                    <p className="text-sm font-mono text-slate-700">{fcmConfig.client_email}</p>
                  </div>
                </div>
                <button onClick={() => setShowFCMModal(true)} className="bg-slate-900 text-white px-6 py-3 rounded-xl font-black text-xs uppercase tracking-widest hover:bg-slate-800">Reconfigurar FCM</button>
              </div>
            ) : (
              <div className="text-center py-12">
                <div className="w-20 h-20 bg-amber-50 rounded-3xl flex items-center justify-center mx-auto mb-4"><AlertCircle size={40} className="text-amber-500" /></div>
                <h4 className="font-black text-lg text-slate-900 mb-2">FCM Não Configurado</h4>
                <p className="text-sm text-slate-500 mb-6">Configure o Firebase para enviar notificações push</p>
                <button onClick={() => setShowFCMModal(true)} className="bg-indigo-600 text-white px-8 py-4 rounded-2xl font-black text-xs uppercase tracking-widest hover:bg-indigo-700 shadow-xl">Configurar FCM</button>
              </div>
            )}
          </div>
        </div>
      )}

      {/* MODALS */}

      {/* FCM Modal */}
      {showFCMModal && (
        <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8">
          <div className="bg-white rounded-[2rem] w-full max-w-lg p-8 shadow-2xl">
            <h3 className="text-xl font-black text-slate-900 mb-2">Configurar FCM</h3>
            <p className="text-sm text-slate-500 mb-6">Credenciais do Firebase Service Account</p>
            <div className="space-y-4">
              <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Project ID" value={fcmForm.project_id} onChange={e => setFcmForm({...fcmForm, project_id: e.target.value})} />
              <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Client Email" value={fcmForm.client_email} onChange={e => setFcmForm({...fcmForm, client_email: e.target.value})} />
              <textarea className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-xs font-mono h-32" placeholder="Private Key" value={fcmForm.private_key} onChange={e => setFcmForm({...fcmForm, private_key: e.target.value})} />
              <div className="flex gap-4 pt-4">
                <button onClick={() => setShowFCMModal(false)} className="flex-1 py-4 text-xs font-black text-slate-400 uppercase">Cancelar</button>
                <button onClick={saveFCM} className="flex-[2] bg-indigo-600 text-white py-4 rounded-xl text-xs font-black uppercase shadow-lg">Salvar</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Campaign Modal */}
      {showCampaignModal && (
        <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8 overflow-y-auto">
          <div className="bg-white rounded-[2rem] w-full max-w-2xl p-8 shadow-2xl my-8">
            <h3 className="text-2xl font-black text-slate-900 mb-6">Nova Campanha</h3>
            <div className="space-y-4">
              <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Nome" value={campaignForm.name} onChange={e => setCampaignForm({...campaignForm, name: e.target.value})} />
              <div className="grid grid-cols-2 gap-4">
                <select className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" value={campaignForm.target_type} onChange={e => setCampaignForm({...campaignForm, target_type: e.target.value})}>
                  <option value="user">Usuário</option><option value="group">Grupo</option><option value="all">Todos</option>
                </select>
                {campaignForm.target_type === 'user' && <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-mono" placeholder="User ID" value={campaignForm.target_user_id} onChange={e => setCampaignForm({...campaignForm, target_user_id: e.target.value})} />}
                {campaignForm.target_type === 'group' && (
                  <select className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" value={campaignForm.target_group_id} onChange={e => setCampaignForm({...campaignForm, target_group_id: e.target.value})}>
                    <option value="">Selecione...</option>
                    {groups.map(g => <option key={g.id} value={g.id}>{g.name} ({g.user_count})</option>)}
                  </select>
                )}
              </div>
              <select className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" value={campaignForm.template_id} onChange={e => setCampaignForm({...campaignForm, template_id: e.target.value})}>
                <option value="">Template (opcional)...</option>
                {templates.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
              </select>
              {!campaignForm.template_id && (
                <>
                  <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Título" value={campaignForm.title} onChange={e => setCampaignForm({...campaignForm, title: e.target.value})} />
                  <textarea className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm min-h-[80px]" placeholder="Mensagem" value={campaignForm.body} onChange={e => setCampaignForm({...campaignForm, body: e.target.value})} />
                </>
              )}
              <select className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" value={campaignForm.language} onChange={e => setCampaignForm({...campaignForm, language: e.target.value})}>
                <option value="pt">Português</option><option value="en">English</option><option value="es">Español</option>
              </select>
              <div className="flex gap-4 pt-4">
                <button onClick={() => setShowCampaignModal(false)} className="flex-1 py-4 text-xs font-black text-slate-400 uppercase">Cancelar</button>
                <button onClick={createCampaign} className="flex-[2] bg-indigo-600 text-white py-4 rounded-xl text-xs font-black uppercase shadow-lg flex items-center justify-center gap-2"><Send size={16} /> Criar</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Template Modal */}
      {showTemplateModal && (
        <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8">
          <div className="bg-white rounded-[2rem] w-full max-w-2xl p-8 shadow-2xl">
            <h3 className="text-2xl font-black text-slate-900 mb-6">Novo Template I18N</h3>
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Código" value={templateForm.code} onChange={e => setTemplateForm({...templateForm, code: e.target.value})} />
                <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Nome" value={templateForm.name} onChange={e => setTemplateForm({...templateForm, name: e.target.value})} />
              </div>
              <div className="flex items-center gap-2">
                <input type="checkbox" id="templateActive" checked={templateForm.active} onChange={e => setTemplateForm({...templateForm, active: e.target.checked})} className="w-4 h-4" />
                <label htmlFor="templateActive" className="text-sm font-bold text-slate-700">Ativo</label>
              </div>
              <div className="p-4 bg-indigo-50 rounded-2xl space-y-3">
                <h5 className="text-xs font-black text-indigo-900 uppercase">Conteúdo Português</h5>
                <input className="w-full bg-white border border-indigo-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Título" value={templateForm.content_i18n.pt.title} onChange={e => setTemplateForm({...templateForm, content_i18n: {...templateForm.content_i18n, pt: {...templateForm.content_i18n.pt, title: e.target.value}}})} />
                <textarea className="w-full bg-white border border-indigo-200 rounded-xl py-3 px-4 text-sm min-h-[80px]" placeholder="Mensagem (use {{var}})" value={templateForm.content_i18n.pt.body} onChange={e => setTemplateForm({...templateForm, content_i18n: {...templateForm.content_i18n, pt: {...templateForm.content_i18n.pt, body: e.target.value}}})} />
              </div>
              <div className="flex gap-4 pt-4">
                <button onClick={() => setShowTemplateModal(false)} className="flex-1 py-4 text-xs font-black text-slate-400 uppercase">Cancelar</button>
                <button onClick={createTemplate} className="flex-[2] bg-indigo-600 text-white py-4 rounded-xl text-xs font-black uppercase shadow-lg">Criar Template</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Group Modal */}
      {showGroupModal && (
        <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8">
          <div className="bg-white rounded-[2rem] w-full max-w-lg p-8 shadow-2xl">
            <h3 className="text-2xl font-black text-slate-900 mb-6">Novo Grupo</h3>
            <div className="space-y-4">
              <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Nome" value={groupForm.name} onChange={e => setGroupForm({...groupForm, name: e.target.value})} />
              <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm" placeholder="Descrição" value={groupForm.description} onChange={e => setGroupForm({...groupForm, description: e.target.value})} />
              <div className="p-4 bg-violet-50 rounded-2xl">
                <h5 className="text-xs font-black text-violet-900 uppercase mb-2">Filtro</h5>
                <p className="text-[10px] text-violet-600">{groupForm.filter_config?.conditions?.length || 0} condições configuradas</p>
                <button onClick={() => setGroupForm({...groupForm, filter_config: { ...groupForm.filter_config, conditions: [...(groupForm.filter_config?.conditions || []), { field: '', op: 'eq', value: '' }] }})} className="mt-2 text-xs text-violet-600 hover:text-violet-800 font-bold">+ Adicionar Condição</button>
                {(groupForm.filter_config?.conditions || []).map((cond: any, idx: number) => (
                  <div key={idx} className="flex gap-2 mt-2">
                    <input className="flex-1 bg-white border border-violet-200 rounded-lg py-1 px-2 text-xs" placeholder="Campo" value={cond.field} onChange={e => { const newConds = [...(groupForm.filter_config?.conditions || [])]; newConds[idx].field = e.target.value; setGroupForm({...groupForm, filter_config: { ...groupForm.filter_config, conditions: newConds }}); }} />
                    <select className="bg-white border border-violet-200 rounded-lg py-1 px-2 text-xs" value={cond.op} onChange={e => { const newConds = [...(groupForm.filter_config?.conditions || [])]; newConds[idx].op = e.target.value; setGroupForm({...groupForm, filter_config: { ...groupForm.filter_config, conditions: newConds }}); }}>
                      <option value="eq">=</option><option value="ne">≠</option><option value="gt">&gt;</option><option value="lt">&lt;</option><option value="contains">Contém</option>
                    </select>
                    <input className="flex-1 bg-white border border-violet-200 rounded-lg py-1 px-2 text-xs" placeholder="Valor" value={cond.value} onChange={e => { const newConds = [...(groupForm.filter_config?.conditions || [])]; newConds[idx].value = e.target.value; setGroupForm({...groupForm, filter_config: { ...groupForm.filter_config, conditions: newConds }}); }} />
                    <button onClick={() => { const newConds = (groupForm.filter_config?.conditions || []).filter((_: any, i: number) => i !== idx); setGroupForm({...groupForm, filter_config: { ...groupForm.filter_config, conditions: newConds }}); }} className="text-rose-400 hover:text-rose-600"><Trash2 size={14} /></button>
                  </div>
                ))}
              </div>
              <div className="flex gap-4 pt-4">
                <button onClick={() => setShowGroupModal(false)} className="flex-1 py-4 text-xs font-black text-slate-400 uppercase">Cancelar</button>
                <button onClick={createGroup} className="flex-[2] bg-indigo-600 text-white py-4 rounded-xl text-xs font-black uppercase shadow-lg">Criar Grupo</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Rule Modal */}
      {showRuleModal && (
        <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[500] flex items-center justify-center p-8">
          <div className="bg-white rounded-[2rem] w-full max-w-lg p-8 shadow-2xl">
            <h3 className="text-2xl font-black text-slate-900 mb-6">Nova Regra</h3>
            <div className="space-y-4">
              <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Nome" value={ruleForm.name} onChange={e => setRuleForm({...ruleForm, name: e.target.value})} />
              <div className="grid grid-cols-2 gap-4">
                <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-mono" placeholder="Tabela" value={ruleForm.trigger_table} onChange={e => setRuleForm({...ruleForm, trigger_table: e.target.value})} />
                <select className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-bold" value={ruleForm.trigger_event} onChange={e => setRuleForm({...ruleForm, trigger_event: e.target.value})}>
                  <option value="INSERT">INSERT</option><option value="UPDATE">UPDATE</option><option value="DELETE">DELETE</option>
                </select>
              </div>
              <input className="w-full bg-slate-50 border border-slate-200 rounded-xl py-3 px-4 text-sm font-mono" placeholder="Coluna User ID" value={ruleForm.recipient_column} onChange={e => setRuleForm({...ruleForm, recipient_column: e.target.value})} />
              <div className="p-4 bg-amber-50 rounded-2xl space-y-3">
                <h5 className="text-xs font-black text-amber-900 uppercase">Template Notificação</h5>
                <input className="w-full bg-white border border-amber-200 rounded-xl py-3 px-4 text-sm font-bold" placeholder="Título" value={ruleForm.title_template} onChange={e => setRuleForm({...ruleForm, title_template: e.target.value})} />
                <textarea className="w-full bg-white border border-amber-200 rounded-xl py-3 px-4 text-sm min-h-[80px]" placeholder="Mensagem" value={ruleForm.body_template} onChange={e => setRuleForm({...ruleForm, body_template: e.target.value})} />
              </div>
              <div className="flex gap-4 pt-4">
                <button onClick={() => setShowRuleModal(false)} className="flex-1 py-4 text-xs font-black text-slate-400 uppercase">Cancelar</button>
                <button onClick={createRule} className="flex-[2] bg-indigo-600 text-white py-4 rounded-xl text-xs font-black uppercase shadow-lg">Criar Regra</button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default PushManager;
