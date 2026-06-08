
import React, { useState, useEffect, useCallback } from 'react';
import {
  Activity, Terminal, Filter, RefreshCw,
  ChevronRight, Circle, Clock, Database, Globe, Loader2,
  Search, ShieldAlert, Trash2, Download, X, Eye,
  Settings2, Calendar, Lock, Globe2, Cpu, ArrowRight,
  CheckCircle2, Code, ShieldCheck, EyeOff, AlertTriangle, Zap, AlertCircle, Cloud,
  HardDrive, BarChart3, ChevronLeft, ChevronRight as ChevronRightIcon, FileSpreadsheet,
  TrendingUp, TrendingDown, Minus, Server
} from 'lucide-react';
import LogExportModal from '../components/logs/LogExportModal';

// ==========================================
// MERGE ADITIVO: Backend Powerful + Frontend UXx
// ==========================================

interface LogEntry {
  id: string;
  method: string;
  path: string;
  status_code: number;
  client_ip: string;
  duration_ms: number;
  user_role: string;
  payload: any;
  headers: any;
  geo_info: any;
  response_size: number;
  created_at: string;
}

interface LogsResponse {
  data: LogEntry[];
  pagination: {
    limit: number;
    offset: number;
    total_count: number;
    has_more: boolean;
    returned: number;
  };
  filters: any;
  project_slug: string;
  queried_at: string;
}

interface LogsStats {
  time_range_hours: number;
  project_slug: string;
  generated_at: string;
  total_requests: number;
  requests_by_method: { method: string; count: number }[];
  status_distribution: { status_code: number; count: number }[];
  top_paths: { path: string; count: number; avg_duration: number }[];
  error_count: number;
  error_rate_percent: number;
  avg_response_time_ms: number;
  peak_rps: number;
}

// ==========================================
// MERGE ADITIVO: Backend Powerful + Frontend UX
// ==========================================

interface LogEntry {
  id: string;
  method: string;
  path: string;
  status_code: number;
  client_ip: string;
  duration_ms: number;
  user_role: string;
  payload: any;
  headers: any;
  geo_info: any;
  response_size: number;
  created_at: string;
}

interface LogsResponse {
  data: LogEntry[];
  pagination: {
    limit: number;
    offset: number;
    total_count: number;
    has_more: boolean;
    returned: number;
  };
  filters: any;
  project_slug: string;
  queried_at: string;
}

interface LogsStats {
  time_range_hours: number;
  project_slug: string;
  generated_at: string;
  total_requests: number;
  requests_by_method: { method: string; count: number }[];
  status_distribution: { status_code: number; count: number }[];
  top_paths: { path: string; count: number; avg_duration: number }[];
  error_count: number;
  error_rate_percent: number;
  avg_response_time_ms: number;
  peak_rps: number;
}

const ProjectLogs: React.FC<{ projectId: string }> = ({ projectId }) => {
  // ============== ESTADO EXISTENTE (PRESERVADO) ==============
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [project, setProject] = useState<any>(null);
  const [currentUserIp, setCurrentUserIp] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedLog, setSelectedLog] = useState<LogEntry | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [settingsTab, setSettingsTab] = useState<'general' | 'firewall' | 'analytics'>('general');
  const [hideInternal, setHideInternal] = useState(true);
  const [showLogExport, setShowLogExport] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [success, setSuccess] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [exportingCloud, setExportingCloud] = useState(false);

  // ============== NOVO: PAGINAÇÃO E FILTROS AVANÇADOS ==============
  const [pagination, setPagination] = useState({
    limit: 100,
    offset: 0,
    total_count: 0,
    has_more: false
  });

  // Filtros avançados
  const [filters, setFilters] = useState({
    method: '',
    path: '',
    client_ip: '',
    client_ip_mode: 'include' as 'include' | 'exclude', // 'include' = mostrar apenas, 'exclude' = excluir da lista
    user_role: '',
    status_code: '',
    start_date: '',
    end_date: '',
    min_duration_ms: '',
    max_duration_ms: ''
  });
  const [showFilters, setShowFilters] = useState(false);

  // Estatísticas
  const [stats, setStats] = useState<LogsStats | null>(null);
  const [loadingStats, setLoadingStats] = useState(false);

  // ============== FETCH DATA ATUALIZADO (SINERGIA BACKEND) ==============
  const fetchData = useCallback(async (resetOffset = false) => {
    setLoading(true);
    try {
      const token = localStorage.getItem('cascata_token');
      const headers = { 'Authorization': `Bearer ${token}`, 'x-cascata-client': 'dashboard' };

      // Build query params with filters
      const params = new URLSearchParams();
      params.set('limit', String(pagination.limit));
      params.set('offset', resetOffset ? '0' : String(pagination.offset));

      if (filters.method) params.set('method', filters.method);
      if (filters.path) params.set('path', filters.path);
      if (filters.client_ip) {
        params.set('client_ip', filters.client_ip);
        params.set('client_ip_mode', filters.client_ip_mode);
      }
      if (filters.user_role) params.set('user_role', filters.user_role);
      if (filters.status_code) params.set('status_code', filters.status_code);
      if (filters.start_date) params.set('start_date', filters.start_date);
      if (filters.end_date) params.set('end_date', filters.end_date);
      if (filters.min_duration_ms) params.set('min_duration_ms', filters.min_duration_ms);
      if (filters.max_duration_ms) params.set('max_duration_ms', filters.max_duration_ms);

      const [logsRes, projectsRes, ipRes] = await Promise.all([
        fetch(`/api/data/${projectId}/logs?${params.toString()}`, { headers }),
        fetch('/api/control/projects', { headers }),
        fetch('/api/control/me/ip', { headers })
      ]);

      const logsData: LogsResponse = await logsRes.json();
      const projectsData = await projectsRes.json();
      const ipData = await ipRes.json();

      if (logsData.data && Array.isArray(logsData.data)) {
        setLogs(logsData.data);
        setPagination(logsData.pagination);
      } else {
        // Fallback para formato antigo (array direto)
      if (logsData.data && Array.isArray(logsData.data)) {
        setLogs(logsData.data);
        setPagination(logsData.pagination);
      } else {
        // Fallback para formato antigo (array direto)
        setLogs(Array.isArray(logsData) ? logsData : []);
      }

      }

      setProject(projectsData.find((p: any) => p.slug === projectId));
      setCurrentUserIp(ipData.ip);
    } catch (err) {
      console.error('Telemetria offline:', err);
    } finally {
      setLoading(false);
    }
  }, [projectId, pagination.limit, pagination.offset, filters]);

  // Fetch estatísticas
  const fetchStats = useCallback(async (hours = 24) => {
    setLoadingStats(true);
    try {
      const token = localStorage.getItem('cascata_token');
      const res = await fetch(`/api/data/${projectId}/logs/stats?hours=${hours}`, {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      const data: LogsStats = await res.json();
      setStats(data);
    } catch (err) {
      console.error('Stats error:', err);
    } finally {
      setLoadingStats(false);
    }
  }, [projectId, pagination.limit, pagination.offset, filters]);

  // ============== CONTROLES DE PAGINAÇÃO ==============
  const handleNextPage = () => {
    if (pagination.has_more) {
      setPagination(prev => ({ ...prev, offset: prev.offset + prev.limit }));
      fetchData();
    }
  };

  const handlePrevPage = () => {
    if (pagination.offset > 0) {
      setPagination(prev => ({ ...prev, offset: Math.max(0, prev.offset - prev.limit) }));
      fetchData();
    }
  };

  const handleApplyFilters = () => {
    setPagination(prev => ({ ...prev, offset: 0 }));
    fetchData(true);
    setShowFilters(false);
  };

  const handleClearFilters = () => {
    setFilters({
      method: '', path: '', client_ip: '', client_ip_mode: 'include', user_role: '', status_code: '',
      start_date: '', end_date: '', min_duration_ms: '', max_duration_ms: ''
    });
    setPagination(prev => ({ ...prev, offset: 0 }));
    fetchData(true);
  };

  // Load initial data only on mount
  useEffect(() => { fetchData(true); fetchStats(24); }, []);

  // HELPER: Format Time using Project Timezone
  const formatTime = (isoString: string) => {
     const tz = project?.metadata?.timezone || 'UTC';
     try {
       return new Date(isoString).toLocaleTimeString(undefined, { timeZone: tz });
     } catch { return new Date(isoString).toLocaleTimeString(); }
  };

  const formatDate = (isoString: string) => {
     const tz = project?.metadata?.timezone || 'UTC';
     try {
       return new Date(isoString).toLocaleDateString(undefined, { timeZone: tz });
     } catch { return new Date(isoString).toLocaleDateString(); }
  };

  const formatBytes = (bytes: number) => {
      if (!bytes || bytes === 0) return '0 B';
      const k = 1024;
      const sizes = ['B', 'KB', 'MB', 'GB'];
      const i = Math.floor(Math.log(bytes) / Math.log(k));
      return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  };

  const handleBlockIp = async (ip: string, isInternal: boolean) => {
    // 1. Safety Checks
    if (isInternal) {
      alert("PROTECTION ENGAGED: Cannot block Cascata internal infrastructure.");
      return;
    }

    if (ip === '127.0.0.1' || ip === '::1' || ip.startsWith('172.') || ip.startsWith('10.') || ip.startsWith('192.168.')) {
        alert("PROTECTION ENGAGED: Cannot block local/private network ranges.");
        return;
    }

    if (ip === currentUserIp) {
      if (!confirm(`⚠️ CRITICAL WARNING: This IP (${ip}) matches your current session.\n\nBlocking it will immediately lock you out of the Data API. The Control Panel might still work via proxy, but direct access will fail.\n\nAre you absolutely sure?`)) return;
    } else {
      if (!confirm(`Confirm firewall ban for ${ip}?`)) return;
    }

    setExecuting(true);
    try {
      const response = await fetch(`/api/control/projects/${projectId}/block-ip`, {
        method: 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
        },
        body: JSON.stringify({ ip })
      });
      if (response.ok) {
        setSuccess(`IP ${ip} bloqueado.`);
        fetchData(); // Refresh to update project blocklist
        setTimeout(() => setSuccess(null), 3000);
      }
    } catch (e) {
      setError("Erro ao bloquear IP.");
      setTimeout(() => setError(null), 3000);
    } finally {
      setExecuting(false);
    }
  };

  const handleUnblockIp = async (ip: string) => {
      setExecuting(true);
      try {
          const res = await fetch(`/api/control/projects/${projectId}/blocklist/${ip}`, {
              method: 'DELETE',
              headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
          });
          if (res.ok) {
              setSuccess(`${ip} removido da blocklist.`);
              fetchData();
          }
      } catch (e) { setError("Erro ao desbloquear."); }
      finally { setExecuting(false); }
  };

  const toggleAutoBlock = async () => {
      const current = project?.metadata?.security?.auto_block_401 || false;
      try {
          const newMetadata = { 
              ...(project?.metadata || {}), 
              security: { ...(project?.metadata?.security || {}), auto_block_401: !current } 
          };
          
          await fetch(`/api/control/projects/${projectId}`, {
              method: 'PATCH',
              headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
              body: JSON.stringify({ metadata: newMetadata })
          });
          setProject({...project, metadata: newMetadata});
          setSuccess(!current ? "Auto-Block Enabled" : "Auto-Block Disabled");
          setTimeout(() => setSuccess(null), 2000);
      } catch (e) { setError("Failed to update security settings"); }
  };

  const handleClearLogs = async (days: number) => {
    if (!confirm(`Confirma a exclusão de logs ANTIGOS (anteriores a ${days} dias atrás)?\n\nLogs recentes (últimos ${days} dias) serão preservados.`)) return;
    setExecuting(true);
    try {
      const res = await fetch(`/api/data/${projectId}/logs?days=${days}`, {
        method: 'DELETE',
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      if (!res.ok) throw new Error('Falha na limpeza');
      const data = await res.json();
      setSuccess(`Limpeza concluída. ${data.deleted_count || ''} logs removidos.`);
      fetchData();
      setTimeout(() => setSuccess(null), 3000);
    } catch (e) {
      setError("Erro na limpeza de logs");
      setTimeout(() => setError(null), 3000);
    } finally {
      setExecuting(false);
    }
  };

  const updateRetention = async (days: number) => {
      // Optimistic update
      setProject({ ...project, log_retention_days: days });
      try {
          await fetch(`/api/control/projects/${projectId}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
            body: JSON.stringify({ log_retention_days: days })
          });
      } catch (e) { fetchData(); /* Revert on error */ }
  };

  // Export local (mantido para compatibilidade - exporta apenas logs carregados na tela)
  const handleExportLocal = () => {
    const dataStr = JSON.stringify(logs, null, 2);
    const blob = new Blob([dataStr], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `logs_${projectId}_${new Date().toISOString()}.json`;
    link.click();
  };

  // Export server-side (novo) - usa backend /logs/export para compliance
  const handleExportServerSide = async (format: 'json' | 'csv', days: number) => {
    setExecuting(true);
    try {
      const token = localStorage.getItem('cascata_token');
      const res = await fetch(`/api/data/${projectId}/logs/export?format=${format}&days=${days}`, {
        headers: { 'Authorization': `Bearer ${token}` }
      });

      if (!res.ok) throw new Error('Export failed');

      // Download do arquivo
      const blob = await res.blob();
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;

      // Extrair filename do header Content-Disposition ou gerar
      const contentDisposition = res.headers.get('content-disposition');
      let filename = `audit_logs_${projectId}_${new Date().toISOString().slice(0,10)}.${format}`;
      if (contentDisposition) {
        const match = contentDisposition.match(/filename="?([^"]*)"?/);
        if (match) filename = match[1];
      }

      link.download = filename;
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
      window.URL.revokeObjectURL(url);

      setSuccess(`Exportação ${format.toUpperCase()} concluída!`);
      setTimeout(() => setSuccess(null), 3000);
    } catch (e) {
      setError('Erro na exportação server-side');
      setTimeout(() => setError(null), 3000);
    } finally {
      setExecuting(false);
    }
  };

  const handleCloudBackup = async () => {
      setExportingCloud(true);
      try {
          const res = await fetch(`/api/control/projects/${projectId}/logs/export-cloud`, {
              method: 'POST',
              headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
          });
          const data = await res.json();
          if (res.ok && data.url) {
              setSuccess("Exportação para nuvem concluída!");
              // Opcional: Abrir o link para o usuário verificar
              if (confirm("Exportação concluída. Deseja abrir o arquivo no provedor de nuvem?")) {
                  window.open(data.url, '_blank');
              }
          } else {
              setError(data.error || "Falha na exportação para nuvem. Verifique se há uma política de backup ativa.");
          }
      } catch (e: any) {
          setError("Erro de conexão ao exportar.");
      } finally {
          setExportingCloud(false);
          setTimeout(() => { setSuccess(null); setError(null); }, 4000);
      }
  };

  // ============== FILTROS E PAGINAÇÃO (SINERGIA BACKEND) ==============
  const filteredLogs = hideInternal 
    ? logs.filter((l: LogEntry) => !l.geo_info?.is_internal)
    : logs;

  // Helper para mostrar contador de filtros ativos
  const activeFilterCount = Object.values(filters).filter(v => v !== '').length;

  const getActionBadge = (log: LogEntry) => {
      const action = log.geo_info?.semantic_action;
      if (action) {
          if (action.includes('DROP') || action.includes('DELETE')) return <span className="bg-rose-100 text-rose-700 px-2 py-0.5 rounded text-[9px] font-black">{action.replace('_', ' ')}</span>;
          if (action.includes('CREATE') || action.includes('INSERT')) return <span className="bg-emerald-100 text-emerald-700 px-2 py-0.5 rounded text-[9px] font-black">{action.replace('_', ' ')}</span>;
          if (action.includes('AUTH')) return <span className="bg-amber-100 text-amber-700 px-2 py-0.5 rounded text-[9px] font-black">{action.replace('_', ' ')}</span>;
          return <span className="bg-indigo-50 text-indigo-600 px-2 py-0.5 rounded text-[9px] font-black">{action.replace('_', ' ')}</span>;
      }
      return <span className={`px-2 py-1 rounded-lg text-[9px] font-black uppercase ${log.method === 'GET' ? 'bg-emerald-50 text-emerald-600' : 'bg-indigo-50 text-indigo-600'}`}>{log.method}</span>;
  };

  return (
    <div className="flex h-full flex-col bg-[#F8FAFC] overflow-hidden">
      {/* Notifications */}
      {success && (
        <div className="fixed top-8 left-1/2 -translate-x-1/2 z-[600] bg-indigo-600 text-white px-8 py-4 rounded-[2rem] shadow-2xl animate-bounce flex items-center gap-3">
          <CheckCircle2 size={18} />
          <span className="text-xs font-black uppercase tracking-widest">{success}</span>
        </div>
      )}
      {error && (
        <div className="fixed top-8 left-1/2 -translate-x-1/2 z-[600] bg-rose-600 text-white px-8 py-4 rounded-[2rem] shadow-2xl animate-pulse flex items-center gap-3">
          <AlertTriangle size={18} />
          <span className="text-xs font-black uppercase tracking-widest">{error}</span>
        </div>
      )}

      <header className="px-10 py-8 bg-white border-b border-slate-200 flex items-center justify-between shrink-0 shadow-sm z-10">
        <div className="flex items-center gap-6">
          <div className="w-14 h-14 bg-slate-900 text-white rounded-[1.5rem] flex items-center justify-center shadow-xl">
            <Activity size={28} />
          </div>
          <div>
            <h2 className="text-3xl font-black text-slate-900 tracking-tighter leading-none">Observability Hub</h2>
            <p className="text-[10px] text-indigo-600 font-bold uppercase tracking-[0.2em] mt-1">Deep API Telemetry & Traffic Insights</p>
          </div>
        </div>
        
        <div className="flex items-center gap-4">
          <div className="flex items-center bg-slate-100 p-1.5 rounded-2xl">
             <button 
                onClick={() => setHideInternal(!hideInternal)} 
                className={`p-3 transition-all rounded-xl flex items-center gap-2 text-[10px] font-black uppercase tracking-widest ${hideInternal ? 'text-slate-400' : 'bg-white shadow-sm text-indigo-600'}`}
                title={hideInternal ? "Mostrar tráfego interno (Dashboard)" : "Ocultar tráfego interno"}
              >
                {hideInternal ? <EyeOff size={18} /> : <Eye size={18} />}
                {hideInternal ? 'INTERNAL HIDDEN' : 'INTERNAL VISIBLE'}
             </button>
             <div className="w-[1px] h-6 bg-slate-200 mx-1"></div>
             <button onClick={() => fetchData()} className="p-3 text-slate-500 hover:text-indigo-600 transition-all"><RefreshCw size={20} className={loading ? 'animate-spin' : ''} /></button>
             <button onClick={() => setShowFilters(true)} className={`p-3 transition-all ${activeFilterCount > 0 ? 'text-indigo-600' : 'text-slate-500 hover:text-indigo-600'}`} title="Filtros Avançados">
                <Filter size={20} />
                {activeFilterCount > 0 && <span className="absolute -top-1 -right-1 w-4 h-4 bg-indigo-600 text-white text-[8px] rounded-full flex items-center justify-center">{activeFilterCount}</span>}
             </button>
             <button onClick={handleExportLocal} className="p-3 text-slate-500 hover:text-indigo-600 transition-all" title="Export Local (JSON)"><Download size={20} /></button>
             <button onClick={() => setShowLogExport(true)} className="p-3 text-slate-500 hover:text-indigo-600 transition-all" title="OpenTelemetry Export"><Server size={20} /></button>
             <button onClick={() => setShowSettings(true)} className="p-3 text-slate-500 hover:text-indigo-600 transition-all"><Settings2 size={20} /></button>
          </div>
        </div>
      </header>

      <div className="flex-1 overflow-hidden flex">
        <main className="flex-1 overflow-y-auto px-10 py-10">
          <div className="bg-white border border-slate-200 rounded-[3rem] shadow-sm overflow-hidden">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr className="bg-slate-50/50 border-b border-slate-100">
                  <th className="px-8 py-6 text-[10px] font-black text-slate-400 uppercase tracking-widest">Snapshot</th>
                  <th className="px-8 py-6 text-[10px] font-black text-slate-400 uppercase tracking-widest">Action</th>
                  <th className="px-8 py-6 text-[10px] font-black text-slate-400 uppercase tracking-widest text-center">Identity</th>
                  <th className="px-8 py-6 text-[10px] font-black text-slate-400 uppercase tracking-widest text-center">Status</th>
                  <th className="px-8 py-6 text-[10px] font-black text-slate-400 uppercase tracking-widest text-right">Data Out</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-50">
                {filteredLogs.length === 0 && !loading ? (
                  <tr>
                    <td colSpan={5} className="py-40 text-center">
                      <Terminal size={64} className="mx-auto text-slate-100 mb-6" />
                      <p className="text-sm font-black text-slate-300 uppercase tracking-widest">Awaiting first request...</p>
                    </td>
                  </tr>
                ) : filteredLogs.map((log) => {
                  const isInternal = log.geo_info?.is_internal;
                  // More aggressive highlighting for 401/403
                  const isAuthFail = log.status_code === 401 || log.status_code === 403;
                  const isError = log.status_code >= 400;
                  // Exfiltration Alert: Large responses (> 1MB) get red text
                  const isLarge = log.response_size > 1024 * 1024;
                  
                  return (
                    <tr 
                      key={log.id} 
                      onClick={() => setSelectedLog(log)}
                      className={`transition-all cursor-pointer group 
                        ${selectedLog?.id === log.id ? 'bg-indigo-50' : 'hover:bg-indigo-50/30'} 
                        ${isInternal ? 'opacity-60 grayscale' : ''} 
                        ${isAuthFail ? 'bg-rose-50/50 hover:bg-rose-100/50' : ''}
                      `}
                    >
                      <td className="px-8 py-5">
                        <div className="flex flex-col">
                          {/* USE PROJECT TIMEZONE FORMATTER */}
                          <span className={`text-xs font-bold ${isAuthFail ? 'text-rose-600' : 'text-slate-900'}`}>{formatTime(log.created_at)}</span>
                          <span className="text-[9px] font-black text-slate-400 uppercase tracking-tight">{formatDate(log.created_at)}</span>
                        </div>
                      </td>
                      <td className="px-8 py-5">
                        <div className="flex items-center gap-3">
                          {getActionBadge(log)}
                          <div className="flex flex-col">
                            <code className={`text-sm font-mono font-bold truncate max-w-[200px] ${isAuthFail ? 'text-rose-700' : 'text-slate-600'}`}>{log.path}</code>
                            {isInternal && <span className="text-[8px] font-black text-indigo-400 uppercase tracking-widest flex items-center gap-1"><ShieldCheck size={8} /> INTERNAL</span>}
                          </div>
                        </div>
                      </td>
                      <td className="px-8 py-5 text-center">
                        <span className={`text-[10px] font-black uppercase tracking-widest px-3 py-1 rounded-full border ${isAuthFail ? 'bg-rose-100 text-rose-700 border-rose-200' : 'bg-slate-50 text-slate-400 border-slate-100'}`}>
                          {log.user_role}
                        </span>
                      </td>
                      <td className="px-8 py-5 text-center">
                        <div className={`inline-flex items-center gap-2 px-3 py-1.5 rounded-xl border ${isError ? 'bg-rose-50 text-rose-600 border-rose-100' : 'bg-emerald-50 text-emerald-600 border-emerald-100'}`}>
                          {isError ? <AlertCircle size={10} /> : <Circle size={8} className="fill-emerald-500" />}
                          <span className="font-black text-xs">{log.status_code}</span>
                        </div>
                      </td>
                      <td className="px-8 py-5 text-right">
                         <div className="flex flex-col items-end">
                             <span className={`text-xs font-mono font-black ${isLarge ? 'text-rose-600' : 'text-slate-600'}`}>
                                {formatBytes(log.response_size)}
                             </span>
                             <span className="text-[9px] text-slate-400 font-bold">{log.duration_ms}ms</span>
                         </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>

            {/* PAGINATION CONTROLS */}
            <div className="flex items-center justify-between px-8 py-4 bg-slate-50 border-t border-slate-100">
              <div className="text-[10px] text-slate-500 font-bold">
                Mostrando {pagination.returned || logs.length} de {pagination.total_count || logs.length} registros
                {pagination.total_count > 0 && ` (offset: ${pagination.offset})`}
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={handlePrevPage}
                  disabled={pagination.offset === 0}
                  className="p-2 rounded-xl bg-white border border-slate-200 text-slate-600 hover:bg-indigo-50 hover:border-indigo-200 hover:text-indigo-600 disabled:opacity-50 disabled:cursor-not-allowed transition-all"
                >
                  <ChevronLeft size={18} />
                </button>
                <span className="text-[10px] font-black text-slate-500 px-3">
                  Página {Math.floor(pagination.offset / pagination.limit) + 1}
                </span>
                <button
                  onClick={handleNextPage}
                  disabled={!pagination.has_more}
                  className="p-2 rounded-xl bg-white border border-slate-200 text-slate-600 hover:bg-indigo-50 hover:border-indigo-200 hover:text-indigo-600 disabled:opacity-50 disabled:cursor-not-allowed transition-all"
                >
                  <ChevronRightIcon size={18} />
                </button>
              </div>
            </div>
          </div>
        </main>

        {/* LOG DETAILS DRAWER */}
        {selectedLog && (
          <aside className="w-[500px] bg-white border-l border-slate-200 overflow-y-auto animate-in slide-in-from-right duration-300 flex flex-col shadow-2xl relative z-20">
            <header className="p-10 border-b border-slate-100 flex items-center justify-between bg-slate-50/50">
              <div className="flex items-center gap-4">
                <div className={`w-12 h-12 rounded-2xl flex items-center justify-center text-white ${selectedLog.status_code >= 400 ? 'bg-rose-600' : 'bg-emerald-600'}`}>
                   <Activity size={24} />
                </div>
                <div>
                   <h3 className="text-xl font-black text-slate-900 tracking-tight">Request DNA</h3>
                   <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest">ID: {selectedLog.id.slice(0, 8)}...</p>
                </div>
              </div>
              <button onClick={() => setSelectedLog(null)} className="p-3 hover:bg-slate-200 rounded-full transition-all text-slate-400"><X size={24}/></button>
            </header>

            <div className="p-10 space-y-10">
              {/* Security Block Action with Intelligence */}
              <div className={`rounded-[2.5rem] p-8 text-white relative overflow-hidden group ${
                  selectedLog.geo_info?.is_internal ? 'bg-slate-900' : 
                  (selectedLog.client_ip === currentUserIp ? 'bg-slate-950' : 
                  (selectedLog.status_code >= 400 ? 'bg-rose-900 shadow-[0_20px_40px_rgba(225,29,72,0.2)]' : 'bg-rose-600'))
                }`}>
                <ShieldAlert className="absolute -bottom-4 -right-4 w-32 h-32 opacity-10 group-hover:scale-125 transition-transform" />
                <h4 className="font-black uppercase text-xs tracking-widest mb-1">Source Governance</h4>
                <p className="text-[10px] font-medium opacity-80 mb-6 flex items-center gap-2">
                  IP: {selectedLog.client_ip} 
                  {selectedLog.client_ip === currentUserIp && <span className="bg-white/20 px-2 py-0.5 rounded-lg border border-white/10 font-bold uppercase tracking-wider text-[8px]">(Current Session)</span>}
                </p>
                
                <button 
                  onClick={() => handleBlockIp(selectedLog.client_ip, selectedLog.geo_info?.is_internal)}
                  disabled={project?.blocklist?.includes(selectedLog.client_ip) || selectedLog.geo_info?.is_internal}
                  className="w-full bg-white text-slate-900 py-4 rounded-2xl text-[10px] font-black uppercase tracking-widest flex items-center justify-center gap-3 shadow-2xl hover:bg-slate-50 transition-all active:scale-95 disabled:opacity-50"
                >
                  {selectedLog.geo_info?.is_internal ? (
                    <><Lock size={14}/> IP INTERNO PROTEGIDO</>
                  ) : project?.blocklist?.includes(selectedLog.client_ip) ? (
                    <><Lock size={14}/> IP JÁ BLOQUEADO</>
                  ) : (
                    <><ShieldAlert size={14} className="text-rose-600"/> BLOQUEAR ORIGEM</>
                  )}
                </button>
              </div>

              {/* Rich Metadata Sections */}
              <div className="space-y-8">
                <DetailSection icon={<HardDrive size={16}/>} label="Data Transfer">
                   <div className="grid grid-cols-2 gap-4">
                     <InfoBox label="Response Size" value={formatBytes(selectedLog.response_size)} />
                     <InfoBox label="Latency" value={`${selectedLog.duration_ms}ms`} />
                   </div>
                </DetailSection>

                <DetailSection icon={<Globe2 size={16}/>} label="Security Context">
                   <div className="grid grid-cols-2 gap-4">
                     <InfoBox label="Auth Result" value={selectedLog.status_code >= 400 ? 'DENIED' : 'GRANTED'} />
                     <InfoBox label="Resolved Role" value={selectedLog.user_role || 'NONE'} />
                   </div>
                </DetailSection>

                <DetailSection icon={<Code size={16}/>} label="Request Payload">
                   <pre className="bg-slate-950 text-emerald-400 p-6 rounded-[2rem] font-mono text-[11px] overflow-auto max-h-60 shadow-inner">
                     {JSON.stringify(selectedLog.payload, null, 2)}
                   </pre>
                </DetailSection>

                <DetailSection icon={<Lock size={16}/>} label="System Headers">
                   <pre className="bg-slate-50 border border-slate-100 p-6 rounded-[2rem] font-mono text-[11px] text-slate-600 overflow-auto">
                     {JSON.stringify(selectedLog.headers, null, 2)}
                   </pre>
                </DetailSection>
              </div>
            </div>
          </aside>
        )}
      </div>

      {/* SETTINGS / CLEANUP MODAL */}
      {showSettings && (
        <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-2xl z-[500] flex items-center justify-center p-8 animate-in fade-in duration-300">
           <div className="bg-white rounded-[4rem] w-full max-w-2xl p-12 shadow-2xl border border-slate-200 animate-in zoom-in-95 max-h-[90vh] overflow-y-auto">
              <header className="flex items-center justify-between mb-8">
                 <div className="flex items-center gap-4">
                    <div className="w-12 h-12 bg-indigo-600 text-white rounded-2xl flex items-center justify-center shadow-lg"><Settings2 size={24} /></div>
                    <div>
                        <h3 className="text-3xl font-black text-slate-900 tracking-tighter">Governance</h3>
                        <div className="flex gap-4 mt-2">
                            <button onClick={() => setSettingsTab('general')} className={`text-[10px] font-black uppercase tracking-widest ${settingsTab === 'general' ? 'text-indigo-600 border-b-2 border-indigo-600' : 'text-slate-400'}`}>General</button>
                            <button onClick={() => setSettingsTab('analytics')} className={`text-[10px] font-black uppercase tracking-widest ${settingsTab === 'analytics' ? 'text-indigo-600 border-b-2 border-indigo-600' : 'text-slate-400'}`}>Analytics</button>
                            <button onClick={() => setSettingsTab('firewall')} className={`text-[10px] font-black uppercase tracking-widest ${settingsTab === 'firewall' ? 'text-indigo-600 border-b-2 border-indigo-600' : 'text-slate-400'}`}>Firewall Rules</button>
                        </div>
                    </div>
                 </div>
                 <button onClick={() => setShowSettings(false)} className="text-slate-300 hover:text-slate-900 transition-colors"><X size={32}/></button>
              </header>

              {settingsTab === 'general' && (
                  <div className="space-y-12">
                     
                     {/* Cloud Backup (Implemented) */}
                     <div className="bg-slate-50 border border-slate-200 p-6 rounded-[2.5rem] flex items-center justify-between">
                        <div className="flex items-center gap-4">
                           <div className="w-12 h-12 bg-white rounded-2xl flex items-center justify-center shadow-sm text-indigo-600"><Cloud size={20}/></div>
                           <div>
                              <h4 className="text-sm font-black text-slate-900 flex items-center gap-2">Cloud Log Export</h4>
                              <p className="text-[10px] text-slate-500 font-bold mt-1">Exportar logs (CSV) para o provedor de backup configurado.</p>
                           </div>
                        </div>
                        <button 
                            onClick={handleCloudBackup}
                            disabled={exportingCloud}
                            className="bg-indigo-600 text-white px-6 py-3 rounded-2xl text-[10px] font-black uppercase tracking-widest flex items-center gap-2 hover:bg-indigo-700 transition-all shadow-lg disabled:opacity-50"
                        >
                            {exportingCloud ? <Loader2 size={14} className="animate-spin"/> : <Download size={14}/>} 
                            Exportar
                        </button>
                     </div>

                     {/* Auto Block Toggle */}
                     <div className="bg-slate-50 border border-slate-200 p-6 rounded-[2.5rem] flex items-center justify-between">
                        <div className="flex items-center gap-4">
                           <div className="w-12 h-12 bg-white rounded-2xl flex items-center justify-center shadow-sm text-indigo-600"><Zap size={20}/></div>
                           <div>
                              <h4 className="text-sm font-black text-slate-900">Auto-Ban Suspicious Origins</h4>
                              <p className="text-[10px] text-slate-500 font-bold mt-1">Automatically add IP to firewall if 401 Unauthorized occurs.</p>
                           </div>
                        </div>
                        <button 
                            onClick={toggleAutoBlock}
                            className={`w-16 h-8 rounded-full p-1 transition-colors ${project?.metadata?.security?.auto_block_401 ? 'bg-indigo-600' : 'bg-slate-200'}`}
                        >
                            <div className={`w-6 h-6 bg-white rounded-full shadow-md transition-transform ${project?.metadata?.security?.auto_block_401 ? 'translate-x-8' : ''}`}></div>
                        </button>
                     </div>

                     <div className="space-y-4">
                        <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">Manual Purge Controls (Apagar Antes de...)</label>
                        <div className="grid grid-cols-3 gap-3">
                           {[3, 7, 15, 30, 60, 90].map(days => (
                             <button 
                               key={days} 
                               onClick={() => handleClearLogs(days)}
                               className="py-4 border border-slate-100 rounded-2xl text-[10px] font-black uppercase tracking-widest text-slate-500 hover:bg-rose-50 hover:text-rose-600 hover:border-rose-200 transition-all flex flex-col items-center gap-1 group"
                             >
                               <Trash2 size={14} className="group-hover:animate-bounce" />
                               {days} Dias Atrás
                             </button>
                           ))}
                        </div>
                     </div>

                     {/* Server-Side Export para Compliance */}
                     <div className="bg-indigo-50 border border-indigo-200 p-6 rounded-[2.5rem]">
                        <h4 className="text-indigo-900 font-black text-sm mb-4 flex items-center gap-2"><FileSpreadsheet size={16}/> Server-Side Export (Compliance)</h4>
                        <p className="text-[10px] text-indigo-600 font-medium mb-4">Exporte todos os logs do período direto do servidor para auditoria.</p>
                        <div className="grid grid-cols-2 gap-3">
                           <button onClick={() => handleExportServerSide('json', 7)} className="py-3 bg-white border border-indigo-200 rounded-xl text-[10px] font-black text-indigo-700 hover:bg-indigo-100 transition-all">
                              JSON (7 dias)
                           </button>
                           <button onClick={() => handleExportServerSide('csv', 7)} className="py-3 bg-white border border-indigo-200 rounded-xl text-[10px] font-black text-indigo-700 hover:bg-indigo-100 transition-all">
                              CSV (7 dias)
                           </button>
                           <button onClick={() => handleExportServerSide('json', 30)} className="py-3 bg-white border border-indigo-200 rounded-xl text-[10px] font-black text-indigo-700 hover:bg-indigo-100 transition-all">
                              JSON (30 dias)
                           </button>
                           <button onClick={() => handleExportServerSide('csv', 30)} className="py-3 bg-white border border-indigo-200 rounded-xl text-[10px] font-black text-indigo-700 hover:bg-indigo-100 transition-all">
                              CSV (30 dias)
                           </button>
                        </div>
                     </div>

                     <div className="p-8 bg-slate-50 border border-slate-100 rounded-[2.5rem] space-y-4">
                        <div className="flex items-center justify-between">
                           <div className="flex items-center gap-3">
                              <Calendar size={18} className="text-indigo-600" />
                              <span className="text-sm font-bold text-slate-800">Retention Strategy</span>
                           </div>
                           <select 
                            value={project?.log_retention_days || 30}
                            onChange={(e) => updateRetention(parseInt(e.target.value))}
                            className="bg-white border-none rounded-xl px-4 py-2 text-xs font-black text-indigo-600 outline-none shadow-sm cursor-pointer"
                           >
                              <option value="7">7 Dias</option>
                              <option value="30">30 Dias</option>
                              <option value="90">90 Dias</option>
                              <option value="365">1 Ano</option>
                           </select>
                        </div>
                     </div>

                     {/* Purge Schedule Configuration */}
                     <div className="p-8 bg-gradient-to-br from-indigo-50 to-purple-50 border border-indigo-200 rounded-[2.5rem] space-y-4">
                        <div className="flex items-center justify-between mb-4">
                           <div className="flex items-center gap-3">
                              <Clock size={18} className="text-indigo-600" />
                              <span className="text-sm font-bold text-indigo-900">Automatic Log Purge</span>
                           </div>
                           <button
                              onClick={async () => {
                                const enabled = !project?.purge_enabled;
                                try {
                                  const token = localStorage.getItem('cascata_token');
                                  await fetch(`/api/data/${projectId}/logs/schedule`, {
                                    method: 'PATCH',
                                    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
                                    body: JSON.stringify({
                                      cron: project?.purge_cron_expression || '0 4 * * *',
                                      timezone: project?.purge_timezone || 'UTC',
                                      enabled
                                    })
                                  });
                                  setProject({ ...project, purge_enabled: enabled });
                                  setSuccess(`Auto-purge ${enabled ? 'enabled' : 'disabled'}`);
                                  setTimeout(() => setSuccess(null), 3000);
                                } catch (e) {
                                  setError('Failed to update purge schedule');
                                  setTimeout(() => setError(null), 3000);
                                }
                              }}
                              className={`w-16 h-8 rounded-full p-1 transition-colors ${project?.purge_enabled ? 'bg-indigo-600' : 'bg-slate-200'}`}
                           >
                              <div className={`w-6 h-6 bg-white rounded-full shadow-md transition-transform ${project?.purge_enabled ? 'translate-x-8' : ''}`}></div>
                           </button>
                        </div>
                        
                        {project?.purge_enabled && (
                          <div className="space-y-3">
                            <div>
                              <label className="text-[10px] font-black text-indigo-600 uppercase tracking-widest">Cron Schedule</label>
                              <input
                                type="text"
                                value={project?.purge_cron_expression || '0 4 * * *'}
                                onChange={async (e) => {
                                  setProject({ ...project, purge_cron_expression: e.target.value });
                                }}
                                onBlur={async (e) => {
                                  try {
                                    const token = localStorage.getItem('cascata_token');
                                    await fetch(`/api/data/${projectId}/logs/schedule`, {
                                      method: 'PATCH',
                                      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
                                      body: JSON.stringify({
                                        cron: e.target.value,
                                        timezone: project?.purge_timezone || 'UTC',
                                        enabled: true
                                      })
                                    });
                                    setSuccess('Purge schedule updated');
                                    setTimeout(() => setSuccess(null), 3000);
                                  } catch (err) {
                                    setError('Failed to update schedule');
                                    setTimeout(() => setError(null), 3000);
                                  }
                                }}
                                placeholder="0 4 * * *"
                                className="w-full mt-1 bg-white border border-indigo-200 rounded-xl px-4 py-2 text-xs font-mono text-indigo-700 outline-none focus:border-indigo-500"
                              />
                              <p className="text-[9px] text-indigo-500 mt-1">Format: minute hour day month weekday (e.g., 0 4 * * * = 4:00 AM daily)</p>
                            </div>
                            
                            <div>
                              <label className="text-[10px] font-black text-indigo-600 uppercase tracking-widest">Timezone</label>
                              <select
                                value={project?.metadata?.timezone || 'UTC'}
                                onChange={async (e) => {
                                  const newTz = e.target.value;
                                  setProject({ ...project, metadata: { ...project?.metadata, timezone: newTz } });
                                  try {
                                    const token = localStorage.getItem('cascata_token');
                                    await fetch(`/api/data/${projectId}/logs/schedule`, {
                                      method: 'PATCH',
                                      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
                                      body: JSON.stringify({
                                        cron: project?.purge_cron_expression || '0 4 * * *',
                                        timezone: newTz,
                                        enabled: true
                                      })
                                    });
                                    setSuccess('Timezone updated');
                                    setTimeout(() => setSuccess(null), 3000);
                                  } catch (err) {
                                    setError('Failed to update timezone');
                                    setTimeout(() => setError(null), 3000);
                                  }
                                }}
                                className="w-full mt-1 bg-white border border-indigo-200 rounded-xl px-4 py-2 text-xs font-bold text-indigo-700 outline-none focus:border-indigo-500"
                              >
                                <option value="UTC">UTC</option>
                                <option value="America/Sao_Paulo">America/Sao Paulo (Brasil)</option>
                                <option value="America/New_York">America/New York</option>
                                <option value="America/Los_Angeles">America/Los Angeles</option>
                                <option value="Europe/London">Europe/London</option>
                                <option value="Europe/Paris">Europe/Paris</option>
                                <option value="Asia/Tokyo">Asia/Tokyo</option>
                                <option value="Asia/Shanghai">Asia/Shanghai</option>
                                <option value="Australia/Sydney">Australia/Sydney</option>
                              </select>
                            </div>
                            
                            <div className="p-3 bg-white/50 rounded-xl">
                              <p className="text-[10px] text-indigo-600">
                                <strong>Next purge:</strong> Based on {project?.purge_cron_expression || '0 4 * * *'} in {project?.metadata?.timezone || 'UTC'}
                              </p>
                              <p className="text-[9px] text-indigo-400 mt-1">
                                Logs older than {project?.log_retention_days || 30} days will be automatically purged.
                              </p>
                            </div>
                          </div>
                        )}
                     </div>
                  </div>
              )}

              {settingsTab === 'analytics' && stats && (
                  <div className="space-y-6">
                      {/* Stats Header */}
                      <div className="bg-gradient-to-br from-indigo-600 to-purple-600 text-white p-6 rounded-[2.5rem]">
                          <div className="flex items-center justify-between mb-4">
                              <h4 className="font-black text-sm flex items-center gap-2"><BarChart3 size={16}/> API Analytics (Last {stats.time_range_hours}h)</h4>
                              <span className="text-[10px] bg-white/20 px-3 py-1 rounded-full">{stats.total_requests.toLocaleString()} requests</span>
                          </div>
                          <div className="grid grid-cols-3 gap-4">
                              <div className="text-center">
                                  <div className="text-2xl font-black">{stats.avg_response_time_ms.toFixed(0)}ms</div>
                                  <div className="text-[9px] opacity-70">Avg Response</div>
                              </div>
                              <div className="text-center">
                                  <div className="text-2xl font-black">{stats.error_rate_percent.toFixed(1)}%</div>
                                  <div className="text-[9px] opacity-70">Error Rate</div>
                              </div>
                              <div className="text-center">
                                  <div className="text-2xl font-black">{stats.peak_rps.toFixed(0)}</div>
                                  <div className="text-[9px] opacity-70">Peak RPS</div>
                              </div>
                          </div>
                      </div>

                      {/* Methods Distribution */}
                      <div className="bg-slate-50 border border-slate-200 p-4 rounded-[2rem]">
                          <h5 className="text-[10px] font-black text-slate-500 uppercase tracking-widest mb-3">HTTP Methods</h5>
                          <div className="space-y-2">
                              {stats.requests_by_method.map((m) => (
                                  <div key={m.method} className="flex items-center gap-3">
                                      <span className="text-[10px] font-bold w-12">{m.method}</span>
                                      <div className="flex-1 bg-slate-200 rounded-full h-2">
                                          <div className="bg-indigo-500 h-2 rounded-full" style={{width: `${(m.count / stats.total_requests * 100) || 0}%`}}></div>
                                      </div>
                                      <span className="text-[10px] font-black text-slate-600">{m.count}</span>
                                  </div>
                              ))}
                          </div>
                      </div>

                      {/* Top Paths */}
                      <div className="bg-slate-50 border border-slate-200 p-4 rounded-[2rem]">
                          <h5 className="text-[10px] font-black text-slate-500 uppercase tracking-widest mb-3">Top Endpoints</h5>
                          <div className="space-y-2 max-h-40 overflow-y-auto">
                              {stats.top_paths.slice(0, 5).map((p) => (
                                  <div key={p.path} className="flex items-center justify-between text-[10px]">
                                      <code className="text-slate-600 truncate max-w-[200px]">{p.path}</code>
                                      <div className="flex items-center gap-2">
                                          <span className="font-bold text-indigo-600">{p.count} reqs</span>
                                          <span className="text-slate-400">{p.avg_duration.toFixed(0)}ms</span>
                                      </div>
                                  </div>
                              ))}
                          </div>
                      </div>

                      {/* Status Distribution */}
                      <div className="grid grid-cols-4 gap-2">
                          {stats.status_distribution.slice(0, 8).map((s) => (
                              <div key={s.status_code} className={`p-3 rounded-xl text-center ${s.status_code >= 400 ? 'bg-rose-50 border border-rose-100' : 'bg-emerald-50 border border-emerald-100'}`}>
                                  <div className={`text-lg font-black ${s.status_code >= 400 ? 'text-rose-600' : 'text-emerald-600'}`}>{s.status_code}</div>
                                  <div className="text-[9px] text-slate-500">{s.count}</div>
                              </div>
                          ))}
                     </div>
                  </div>
              )}

              {settingsTab === 'firewall' && (
                  <div className="space-y-6">
                      <div className="bg-rose-50 border border-rose-100 p-6 rounded-[2.5rem]">
                          <h4 className="text-rose-700 font-black text-sm mb-2 flex items-center gap-2"><ShieldAlert size={16}/> Active Blocklist</h4>
                          <p className="text-[10px] text-rose-500 font-medium">IPs listed here are completely blocked from accessing the API.</p>
                      </div>
                      <div className="space-y-2">
                          {project?.blocklist?.length === 0 && <p className="text-center text-slate-400 text-xs py-8">Nenhum IP bloqueado.</p>}
                          {project?.blocklist?.map((ip: string) => (
                              <div key={ip} className="flex items-center justify-between bg-white border border-slate-200 p-4 rounded-2xl">
                                  <span className="text-xs font-mono font-bold text-slate-700">{ip}</span>
                                  <button onClick={() => handleUnblockIp(ip)} className="text-[10px] font-black bg-emerald-100 text-emerald-700 px-3 py-1.5 rounded-xl hover:bg-emerald-200 transition-colors">DESBLOQUEAR</button>
                              </div>
                          ))}
                      </div>
                  </div>
              )}
           </div>
        </div>
      )}

      {/* ADVANCED FILTERS MODAL */}
      {showFilters && (
        <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-2xl z-[500] flex items-center justify-center p-8 animate-in fade-in duration-300">
           <div className="bg-white rounded-[3rem] w-full max-w-4xl p-10 shadow-2xl border border-slate-200 animate-in zoom-in-95 max-h-[90vh] overflow-y-auto">
              <header className="flex items-center justify-between mb-8">
                 <div className="flex items-center gap-4">
                    <div className="w-12 h-12 bg-indigo-600 text-white rounded-2xl flex items-center justify-center shadow-lg"><Filter size={24} /></div>
                    <div>
                        <h3 className="text-2xl font-black text-slate-900 tracking-tighter">Filtros Avançados</h3>
                        <p className="text-[10px] text-slate-500 font-bold mt-1">Filtrar logs por múltiplos critérios simultaneamente</p>
                    </div>
                 </div>
                 <button onClick={() => setShowFilters(false)} className="text-slate-300 hover:text-slate-900 transition-colors"><X size={32}/></button>
              </header>

              <div className="grid grid-cols-2 gap-6 mb-8">
                 {/* Method */}
                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">HTTP Method</label>
                    <select
                       value={filters.method}
                       onChange={(e) => setFilters({...filters, method: e.target.value})}
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    >
                       <option value="">Qualquer</option>
                       <option value="GET">GET</option>
                       <option value="POST">POST</option>
                       <option value="PUT">PUT</option>
                       <option value="PATCH">PATCH</option>
                       <option value="DELETE">DELETE</option>
                    </select>
                 </div>

                 {/* Status Code */}
                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Status Code</label>
                    <input
                       type="number"
                       value={filters.status_code}
                       onChange={(e) => setFilters({...filters, status_code: e.target.value})}
                       placeholder="Ex: 200, 404, 500"
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                 </div>

                 {/* Path */}
                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Path (contém)</label>
                    <input
                       type="text"
                       value={filters.path}
                       onChange={(e) => setFilters({...filters, path: e.target.value})}
                       placeholder="Ex: /users, /api"
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                 </div>

                 {/* Client IP com modo Include/Exclude */}
                 <div className="space-y-2">
                    <div className="flex items-center justify-between">
                       <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Client IP</label>
                       <div className="flex items-center bg-slate-100 rounded-lg p-0.5">
                          <button
                             onClick={() => setFilters({...filters, client_ip_mode: 'include'})}
                             className={`px-2 py-1 rounded-md text-[9px] font-bold transition-all ${filters.client_ip_mode === 'include' ? 'bg-white shadow-sm text-indigo-600' : 'text-slate-500'}`}
                             title="Mostrar apenas estes IPs"
                          >
                             INCLUIR
                          </button>
                          <button
                             onClick={() => setFilters({...filters, client_ip_mode: 'exclude'})}
                             className={`px-2 py-1 rounded-md text-[9px] font-bold transition-all ${filters.client_ip_mode === 'exclude' ? 'bg-white shadow-sm text-rose-600' : 'text-slate-500'}`}
                             title="Excluir estes IPs da lista"
                          >
                             EXCLUIR
                          </button>
                       </div>
                    </div>
                    <input
                       type="text"
                       value={filters.client_ip}
                       onChange={(e) => setFilters({...filters, client_ip: e.target.value})}
                       placeholder="Ex: 192.168.1.1, 10.0.0.5, 172.16.0.1"
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                    <p className="text-[9px] text-slate-400">
                       {filters.client_ip_mode === 'include' 
                          ? "Mostrar apenas requisições destes IPs" 
                          : "Ocultar requisições destes IPs"}
                       {filters.client_ip.includes(',') && " (múltiplos IPs separados por vírgula)"}
                    </p>
                 </div>

                 {/* User Role */}
                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">User Role</label>
                    <input
                       type="text"
                       value={filters.user_role}
                       onChange={(e) => setFilters({...filters, user_role: e.target.value})}
                       placeholder="Ex: admin, user"
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                 </div>

                 {/* Date Range */}
                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Data Início</label>
                    <input
                       type="datetime-local"
                       value={filters.start_date}
                       onChange={(e) => setFilters({...filters, start_date: e.target.value})}
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                 </div>

                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Data Fim</label>
                    <input
                       type="datetime-local"
                       value={filters.end_date}
                       onChange={(e) => setFilters({...filters, end_date: e.target.value})}
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                 </div>

                 {/* Duration Range */}
                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Duração Mín (ms)</label>
                    <input
                       type="number"
                       value={filters.min_duration_ms}
                       onChange={(e) => setFilters({...filters, min_duration_ms: e.target.value})}
                       placeholder="Ex: 100"
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                 </div>

                 <div className="space-y-2">
                    <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Duração Máx (ms)</label>
                    <input
                       type="number"
                       value={filters.max_duration_ms}
                       onChange={(e) => setFilters({...filters, max_duration_ms: e.target.value})}
                       placeholder="Ex: 5000"
                       className="w-full bg-slate-50 border border-slate-200 rounded-xl px-4 py-3 text-xs font-bold text-slate-700 outline-none focus:border-indigo-500"
                    />
                 </div>
              </div>

              <div className="flex items-center justify-between pt-6 border-t border-slate-100">
                 <button
                    onClick={handleClearFilters}
                    className="px-6 py-3 text-[10px] font-black text-slate-500 hover:text-rose-600 transition-colors"
                 >
                    Limpar Filtros
                 </button>
                 <div className="flex gap-3">
                    <button
                       onClick={() => setShowFilters(false)}
                       className="px-8 py-3 border border-slate-200 rounded-xl text-[10px] font-black text-slate-600 hover:bg-slate-50 transition-all"
                    >
                       Cancelar
                    </button>
                    <button
                       onClick={handleApplyFilters}
                       className="px-8 py-3 bg-indigo-600 text-white rounded-xl text-[10px] font-black hover:bg-indigo-700 transition-all shadow-lg"
                    >
                       Aplicar Filtros
                    </button>
                 </div>
              </div>
           </div>
        </div>
      )}

      {/* LOG EXPORT MODAL (OTel) */}
      <LogExportModal
        isOpen={showLogExport}
        onClose={() => setShowLogExport(false)}
        projectId={projectId}
        projectSlug={project?.slug || projectId}
        fetchWithAuth={async (url, options = {}) => {
          const token = localStorage.getItem('cascata_token');
          const res = await fetch(url, {
            ...options,
            headers: {
              'Authorization': `Bearer ${token}`,
              'Content-Type': 'application/json',
              ...options.headers,
            },
          });
          return res;
        }}
        onSuccess={(msg) => { setSuccess(msg); setTimeout(() => setSuccess(null), 3000); }}
        onError={(msg) => { setError(msg); setTimeout(() => setError(null), 3000); }}
      />
    </div>
  );
};

const DetailSection: React.FC<{ icon: React.ReactNode, label: string, children: React.ReactNode }> = ({ icon, label, children }) => (
  <div className="space-y-4">
    <div className="flex items-center gap-3 text-slate-400">
      {icon}
      <span className="text-[10px] font-black uppercase tracking-widest">{label}</span>
    </div>
    {children}
  </div>
);

const InfoBox: React.FC<{ label: string, value: string }> = ({ label, value }) => (
  <div className="bg-slate-50 border border-slate-100 rounded-2xl p-4 flex flex-col gap-1">
    <span className="text-[9px] font-black text-slate-400 uppercase tracking-tight">{label}</span>
    <span className="text-xs font-bold text-slate-900 font-mono truncate">{value}</span>
  </div>
);

export default ProjectLogs;
