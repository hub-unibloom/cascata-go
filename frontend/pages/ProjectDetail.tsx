
import React, { useState, useEffect, useMemo } from 'react';
import {
  Shield, Key, Database, Activity, CheckCircle2, Loader2, Server,
  Settings2, Globe, Lock, Workflow, ExternalLink, Power, ArrowRight,
  BookOpen, Zap, BarChart3, AlertCircle, Brain, Cable, Network,
  Cpu, HardDrive, Wifi, Radio, Clock, GitBranch, Copy, RefreshCw, Trash2, Rocket,
  GitMerge, RefreshCcw, AlertOctagon, Plus, Check, Sliders, ShieldCheck, X, AlertTriangle, Code2
} from 'lucide-react';
import {
  AreaChart, Area, Line, XAxis, YAxis, CartesianGrid, Tooltip,
  ResponsiveContainer, BarChart, Bar, Cell, PieChart, Pie
} from 'recharts';
import ProjectSettings from './ProjectSettings';
import ProjectIntelligence from './ProjectIntelligence';
import DeployWizard from '../components/deploy/DeployWizard';

// --- STRICT TYPES ---
type TabType = 'overview' | 'intelligence' | 'settings';

interface ProjectStats {
  tables: number;
  users: number;
  size: string;
  active_connections: number;
  throughput: Array<{ name: string; requests: number; success: number; error: number }>;
}

interface ProjectData {
  id: string;
  name: string;
  slug: string;
  tier: string;
  created_at: string;
  metadata?: any;
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
  // HOURLY DATA for Traffic Pulse Chart
  throughput: { name: string; requests: number; success: number; error: number }[];
}

// --- SUB-COMPONENTS (Hoisted for Safety) ---

const StatCard: React.FC<{
  title: string;
  value: string;
  icon: React.ReactNode;
  label: string;
  trend?: string;
  trendUp?: boolean;
  color?: string;
}> = ({ title, value, icon, label, trend, trendUp, color = "indigo" }) => {
  const colorClasses: Record<string, string> = {
    indigo: "text-indigo-600 bg-indigo-50 border-indigo-100 group-hover:border-indigo-200",
    emerald: "text-emerald-600 bg-emerald-50 border-emerald-100 group-hover:border-emerald-200",
    blue: "text-blue-600 bg-blue-50 border-blue-100 group-hover:border-blue-200",
    amber: "text-amber-600 bg-amber-50 border-amber-100 group-hover:border-amber-200",
    rose: "text-slate-700 bg-slate-100 border-slate-200 group-hover:border-slate-300",
  };

  const bgClass = colorClasses[color] || colorClasses.indigo;

  return (
    <div className="bg-white border border-slate-200 rounded-[2rem] p-5 shadow-sm hover:shadow-xl hover:-translate-y-1 transition-all duration-300 group relative overflow-hidden">
      <div className={`absolute top-0 right-0 p-4 opacity-5 scale-150 transition-transform group-hover:scale-[1.75] duration-500 ${color === 'indigo' ? 'text-indigo-900' : ''}`}>
        {icon}
      </div>
      <div className="relative z-10">
        {/* Linha 1: Icone + Valor lado a lado */}
        <div className="flex items-center gap-3 mb-2">
          <div className={`w-10 h-10 rounded-xl flex items-center justify-center ${bgClass} shadow-inner transition-colors`}>
            {icon}
          </div>
          <div className="text-2xl font-bold text-slate-900 tracking-tight">{value}</div>
          {trend && (
            <div className={`ml-auto px-2 py-1 rounded-lg text-[9px] font-medium flex items-center gap-1 ${trendUp ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-200 text-slate-700'}`}>
              {trendUp ? '↑' : '↓'} {trend}
            </div>
          )}
        </div>
        {/* Linha 2: Titulo / Label */}
        <div className="text-[10px] font-medium text-slate-400 uppercase tracking-wider leading-tight">
          {title} <span className="text-slate-300 mx-1">•</span> <span className="text-slate-500">{label}</span>
        </div>
      </div>
    </div>
  );
};

const QuickAction: React.FC<{
  icon: React.ReactNode;
  label: string;
  desc: string;
  onClick: () => void;
}> = ({ icon, label, desc, onClick }) => (
  <button
    onClick={onClick}
    className="flex items-center gap-4 p-4 rounded-2xl bg-white border border-slate-100 hover:border-indigo-200 hover:shadow-lg hover:bg-slate-50 transition-all text-left group w-full"
  >
    <div className="w-10 h-10 rounded-xl bg-slate-100 flex items-center justify-center text-slate-500 group-hover:bg-indigo-600 group-hover:text-white transition-all shadow-sm">
      {icon}
    </div>
    <div>
      <div className="text-xs font-medium text-slate-900 group-hover:text-indigo-700 transition-colors">{label}</div>
      <div className="text-[10px] text-slate-400 font-normal">{desc}</div>
    </div>
    <div className="ml-auto opacity-0 group-hover:opacity-100 transition-opacity transform translate-x-2 group-hover:translate-x-0">
      <ArrowRight size={14} className="text-indigo-400" />
    </div>
  </button>
);

// --- MAIN COMPONENT ---

const ProjectDetail: React.FC<{ projectId: string }> = ({ projectId }) => {
  const [activeTab, setActiveTab] = useState<TabType>('overview');
  const [stats, setStats] = useState<ProjectStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [projectData, setProjectData] = useState<ProjectData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [lastRefreshed, setLastRefreshed] = useState<Date>(new Date());

  // BRANCHING STATE
  const [branchStatus, setBranchStatus] = useState<any>(null);
  const [showDeployModal, setShowDeployModal] = useState(false);
  const [selectedDeployBranch, setSelectedDeployBranch] = useState<string>('');

  // Logs Analytics State
  const [logsStats, setLogsStats] = useState<LogsStats | null>(null);
  const [loadingLogsStats, setLoadingLogsStats] = useState(false);

  // Timezone State - ciclos: local (padrão) -> tenant -> server -> local
  const [timezoneMode, setTimezoneMode] = useState<'local' | 'tenant' | 'server'>('local');

  // Filter Modal State
  const [showFilterModal, setShowFilterModal] = useState(false);
  const [dateRange, setDateRange] = useState<{ start: string; end: string }>(() => {
    const now = new Date();
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000);
    return {
      start: yesterday.toISOString().slice(0, 16), // YYYY-MM-DDTHH:mm
      end: now.toISOString().slice(0, 16)
    };
  });
  const [isCustomRange, setIsCustomRange] = useState(false);

  const fetchProjectData = async () => {
    try {
      const token = localStorage.getItem('cascata_token');
      const headers = { 'Authorization': `Bearer ${token}` };

      // Parallel Fetching for Performance
      const [statsRes, projRes, branchRes] = await Promise.all([
        fetch(`/api/data/${projectId}/stats`, { headers }),
        fetch('/api/control/projects', { headers }),
        fetch(`/api/data/${projectId}/branch/status`, { headers })
      ]);

      if (!statsRes.ok) throw new Error("Failed to fetch stats");
      if (!projRes.ok) throw new Error("Failed to fetch project info");

      const statsData = await statsRes.json();
      const projects = await projRes.json();
      const branchData = await branchRes.json();

      const current = Array.isArray(projects) ? projects.find((p: any) => p.slug === projectId) : null;

      if (!current) throw new Error("Project not found");

      setStats(statsData);
      setProjectData(current);
      setBranchStatus(branchData);
      setError(null);
      setLastRefreshed(new Date());
    } catch (err: any) {
      console.error('Error fetching data:', err);
      setError(err.message || "Failed to load dashboard data");
    } finally {
      setLoading(false);
    }
  };

  // Fetch logs analytics - suporta hours padrão ou date range personalizado
  const fetchLogsStats = async (hours = 24, customRange?: { start: string; end: string }) => {
    setLoadingLogsStats(true);
    try {
      const token = localStorage.getItem('cascata_token');
      let url = `/api/data/${projectId}/logs/stats?hours=${hours}&interval=30`;

      // Se houver range personalizado, usa start/end em vez de hours
      if (customRange?.start && customRange?.end) {
        url = `/api/data/${projectId}/logs/stats?start=${encodeURIComponent(customRange.start)}&end=${encodeURIComponent(customRange.end)}&interval=30`;
      }

      const res = await fetch(url, {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      const data: LogsStats = await res.json();
      setLogsStats(data);
    } catch (err) {
      console.error('Error fetching logs stats:', err);
    } finally {
      setLoadingLogsStats(false);
    }
  };

  // Aplicar filtro de data personalizado
  const applyDateFilter = () => {
    if (dateRange.start && dateRange.end) {
      setIsCustomRange(true);
      fetchLogsStats(24, dateRange);
      setShowFilterModal(false);
    }
  };

  // Resetar para padrão 24h
  const resetDateFilter = () => {
    setIsCustomRange(false);
    const now = new Date();
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000);
    setDateRange({
      start: yesterday.toISOString().slice(0, 16),
      end: now.toISOString().slice(0, 16)
    });
    fetchLogsStats(24);
    setShowFilterModal(false);
  };

  useEffect(() => {
    fetchProjectData();
    fetchLogsStats(24);
    const interval = setInterval(() => {
      fetchProjectData();
      // Se há filtro custom ativo, mantém ele; senão usa padrão 24h
      if (isCustomRange && dateRange.start && dateRange.end) {
        fetchLogsStats(24, dateRange);
      } else {
        fetchLogsStats(24);
      }
    }, 10000); // 10s auto-refresh
    return () => clearInterval(interval);
  }, [projectId, isCustomRange, dateRange]);

  const openDeployModal = async () => {
    setShowDeployModal(true);
    try {
      const listRes = await fetch(`/api/data/${projectId}/branch/list`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      const listData = await listRes.json();
      const defaultBranch = Array.isArray(listData?.branches)
        ? listData.branches.find((branch: any) => branch.branch_type === 'environment' && !branch.is_main)
        : null;
      if (!defaultBranch?.name) {
        throw new Error('No environment branch available for deploy.');
      }
      setSelectedDeployBranch(defaultBranch.name);
    } catch (e) {
      console.error("Diff failed", e);
    }
  };

  const getBaseUrl = () => {
    if (projectData?.custom_domain) {
      return `https://${projectData.custom_domain}`;
    }
    return `${window.location.origin}/api/data/${projectId}`;
  };

  const isEjected = !!projectData?.metadata?.external_db_url;
  const projectTimezone = projectData?.metadata?.timezone ?? 'UTC';

  // Helper: Converte hora UTC do servidor para o timezone selecionado
  const convertHourToTimezone = (hourStr: string, mode: 'local' | 'tenant' | 'server'): string => {
    if (mode === 'server') return hourStr;

    // Parse hour string (formato "HH:MM" vindo do servidor em UTC)
    const [hours, minutes] = hourStr.split(':').map(Number);

    // Cria uma data representando essa hora em UTC (usando uma data base fixa)
    const utcDate = new Date(Date.UTC(2024, 0, 1, hours || 0, minutes || 0, 0));

    if (mode === 'local') {
      // Converte UTC para horário local do usuário
      return utcDate.toLocaleTimeString('en-US', {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
        timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone
      });
    }

    if (mode === 'tenant' && projectTimezone) {
      // Converte UTC para timezone do tenant
      return utcDate.toLocaleTimeString('en-US', {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
        timeZone: projectTimezone
      });
    }

    return hourStr;
  };

  // Traffic Pulse Chart Data - usa throughput dos logs com conversão de timezone
  const chartData = useMemo(() => {
    let rawData: any[] = [];

    // Primeiro tenta dados dos logs (nova API)
    if (logsStats?.throughput && logsStats.throughput.length > 0) {
      rawData = logsStats.throughput;
    } else if (stats?.throughput) {
      // Fallback para stats antigo (compatibilidade)
      rawData = stats.throughput;
    }

    // Aplica conversão de timezone - suporta formato DD/MM HH:MM
    return rawData.map((item: any) => {
      // Se o nome já inclui data (formato DD/MM HH:MM), não aplica conversão de timezone
      // pois o backend já formatou corretamente
      const hasDateFormat = item.name.includes('/');

      return {
        ...item,
        total: item.requests,
        name: hasDateFormat ? item.name : convertHourToTimezone(item.name, timezoneMode)
      };
    });
  }, [logsStats, stats, timezoneMode, projectTimezone]);

  // Calcula escala máxima para o eixo Y de erros (para garantir visibilidade)
  const maxErrorValue = useMemo(() => {
    if (!chartData.length) return 1;
    const maxErr = Math.max(...chartData.map((d: any) => d.error || 0));
    return maxErr > 0 ? maxErr : 1;
  }, [chartData]);

  // Toggle timezone mode
  const cycleTimezone = () => {
    setTimezoneMode((prev: 'local' | 'tenant' | 'server') => {
      if (prev === 'local') return 'tenant';
      if (prev === 'tenant') return 'server';
      return 'local';
    });
  };

  // Label para o timezone atual
  const getTimezoneLabel = () => {
    if (timezoneMode === 'local') return 'Local Time';
    if (timezoneMode === 'tenant') return `Tenant (${projectTimezone})`;
    return 'Server (UTC)';
  };

  // Status Distribution para Health Monitor - usa dados dos logs
  const statusDistribution = useMemo(() => {
    // Usa logsStats se disponível (nova API)
    if (logsStats) {
      const totalSuccess = logsStats.total_requests - logsStats.error_count;
      const totalError = logsStats.error_count;
      if (logsStats.total_requests === 0) return [];
      return [
        { name: 'Success (2xx)', value: totalSuccess, color: '#10B981' },
        { name: 'Errors (4xx/5xx)', value: totalError, color: '#334155' }
      ];
    }
    // Fallback para stats antigo
    if (!stats?.throughput) return [];
    const totalSuccess = stats.throughput.reduce((acc, cur) => acc + (cur.success || 0), 0);
    const totalError = stats.throughput.reduce((acc, cur) => acc + (cur.error || 0), 0);
    const total = totalSuccess + totalError;
    if (total === 0) return [];
    return [
      { name: 'Success (2xx)', value: totalSuccess, color: '#10B981' },
      { name: 'Errors (4xx/5xx)', value: totalError, color: '#334155' }
    ];
  }, [logsStats, stats]);

  if (loading && !projectData) {
    return (
      <div className="flex h-full flex-col items-center justify-center space-y-4">
        <Loader2 className="animate-spin text-indigo-600" size={48} />
        <p className="text-slate-400 font-bold text-xs uppercase tracking-widest">Initializing Dashboard...</p>
      </div>
    );
  }

  if (error && !projectData) {
    return (
      <div className="flex h-full items-center justify-center flex-col gap-6 text-slate-400 p-10">
        <div className="w-20 h-20 bg-slate-100 rounded-full flex items-center justify-center">
          <AlertCircle size={40} className="text-slate-600" />
        </div>
        <div className="text-center">
          <h3 className="text-xl font-black text-slate-900 mb-2">Connection Error</h3>
          <p className="font-medium text-sm text-slate-500 max-w-md">{error}</p>
        </div>
        <button onClick={() => window.location.reload()} className="px-6 py-3 bg-slate-900 text-white rounded-xl text-xs font-bold uppercase tracking-widest hover:bg-indigo-600 transition-all shadow-lg">Retry Connection</button>
      </div>
    );
  }

  return (
    <div className="pt-4 lg:pt-6 px-8 lg:px-12 max-w-[1920px] mx-auto w-full space-y-8 pb-40">

      {/* HEADER SECTION */}
      <div className="flex flex-col xl:flex-row xl:items-end justify-between gap-8 animate-in slide-in-from-top-4 duration-700">
        <div>
          <h1 className="text-5xl lg:text-6xl font-black text-slate-900 tracking-tighter leading-tight">
            {projectData?.name ?? projectId}
          </h1>
          <div className="flex items-center gap-4 mt-4">
            <div className="flex items-center gap-2 px-3 py-1.5 bg-slate-100 rounded-lg border border-slate-200">
              <Globe size={12} className="text-slate-400" />
              <span className="font-mono text-[10px] font-medium text-slate-600">{projectData?.slug ?? 'unknown-slug'}</span>
            </div>
            <div className="flex items-center gap-2 px-3 py-1.5 bg-indigo-50 rounded-lg border border-indigo-100">
              <Clock size={12} className="text-indigo-400" />
              <span className="font-mono text-[10px] font-medium text-indigo-700">{projectTimezone}</span>
            </div>
            <div className="flex items-center gap-3 mb-2">
              <div className={`w-3 h-3 rounded-full ${isEjected ? 'bg-amber-400' : 'bg-emerald-500'} animate-pulse shadow-[0_0_10px_rgba(16,185,129,0.5)]`}></div>
              <span className="text-[10px] font-medium uppercase tracking-widest text-slate-500">
                {isEjected ? 'External Topology' : 'Managed Infrastructure'}
              </span>
            </div>
          </div>
        </div>

        {/* TAB NAVIGATION */}
        <div className="flex bg-slate-100 p-1.5 rounded-2xl shadow-inner overflow-x-auto max-w-full">
          {[
            { id: 'overview', icon: Activity, label: 'Mission Control' },
            { id: 'intelligence', icon: Brain, label: 'Neural Core (AI)' },
            { id: 'settings', icon: Settings2, label: 'System Config' }
          ].map(tab => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id as TabType)}
              className={`
                    px-6 py-3.5 text-xs font-semibold rounded-xl transition-all flex items-center gap-3 whitespace-nowrap
                    ${activeTab === tab.id
                  ? 'bg-white shadow-lg text-indigo-600 ring-1 ring-black/5 scale-100'
                  : 'text-slate-500 hover:text-slate-800 hover:bg-white/50'}
                `}
            >
              <tab.icon size={16} strokeWidth={2} />
              <span className="uppercase tracking-widest">{tab.label}</span>
            </button>
          ))}
        </div>
      </div>

      {/* DYNAMIC CONTENT AREA */}
      <div className="min-h-[500px]">
        {activeTab === 'overview' && (
          <div className="space-y-10 animate-in fade-in slide-in-from-bottom-8 duration-500">

            {/* KPI STATS GRID */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
              <StatCard title="Identity Records" value={stats?.users?.toString() ?? '0'} icon={<Shield size={24} />} label="auth.users" color="emerald" trend="+2%" trendUp={true} />
              <StatCard title="Volume Usage" value={stats?.size ?? '0 MB'} icon={<HardDrive size={24} />} label="physical disk" color="blue" />
              <StatCard title="Active Sessions" value={stats?.active_connections?.toString() ?? '0'} icon={<Network size={24} />} label="db connections" color="amber" />
              {logsStats && (
                <div className="bg-gradient-to-br from-indigo-600 to-purple-600 text-white p-6 rounded-[2.5rem]">
                  <div className="flex items-center justify-between mb-4">
                    <h4 className="font-semibold text-sm flex items-center gap-2"><BarChart3 size={16} /> API Analytics (Last {logsStats.time_range_hours}h)</h4>
                    <span className="text-[10px] bg-white/20 px-3 py-1 rounded-full font-medium">{logsStats.total_requests.toLocaleString()} requests</span>
                  </div>
                  <div className="grid grid-cols-3 gap-4">
                    <div className="text-center">
                      <div className="text-2xl font-bold">{logsStats.avg_response_time_ms.toFixed(0)}ms</div>
                      <div className="text-[9px] opacity-70 mt-1">Avg Response</div>
                    </div>
                    <div className="text-center">
                      <div className="text-2xl font-bold">{logsStats.error_rate_percent.toFixed(1)}%</div>
                      <div className="text-[9px] opacity-70 mt-1">Error Rate</div>
                    </div>
                    <div className="text-center">
                      <div className="text-2xl font-bold">{logsStats.peak_rps.toFixed(0)}</div>
                      <div className="text-[9px] opacity-70 mt-1">Peak RPS</div>
                    </div>
                  </div>
                </div>

              )}
            </div>


            {/* MAIN DASHBOARD LAYOUT */}
            <div className="grid grid-cols-1 lg:grid-cols-12 gap-8">

              {/* LEFT: CHARTS (8 cols) */}
              <div className="lg:col-span-8 space-y-8">
                {/* Traffic Pulse - Enhanced with Real Logs Data */}
                <div className="bg-white border border-slate-200 rounded-[2.5rem] p-8 shadow-sm relative overflow-hidden group">
                  {/* Ambient glow effect */}
                  <div className="absolute -top-20 -right-20 w-64 h-64 bg-indigo-500/5 rounded-full blur-3xl group-hover:bg-indigo-500/10 transition-all duration-700"></div>
                  <div className="absolute -bottom-20 -left-20 w-64 h-64 bg-slate-500/5 rounded-full blur-3xl group-hover:bg-slate-500/10 transition-all duration-700"></div>

                  <div className="relative z-10">
                    {/* Header with Live Metrics */}
                    <div className="flex items-start justify-between mb-6">
                      <div>
                        <h3 className="text-xl font-bold text-slate-900 tracking-tight flex items-center gap-3">
                          <div className="relative">
                            <Activity size={20} className="text-indigo-600" />
                            {/* Live pulse indicator */}
                            {logsStats && logsStats.total_requests > 0 && (
                              <span className="absolute -top-1 -right-1 w-2 h-2 bg-emerald-500 rounded-full animate-ping"></span>
                            )}
                          </div>
                          Traffic Pulse
                        </h3>
                        <p className="text-slate-400 text-xs font-medium mt-1 uppercase tracking-widest">
                          {logsStats ? `Live Analytics • ${logsStats.time_range_hours}h Window` : 'Initializing...'}
                        </p>
                      </div>

                      {/* Real-time Stats Pills */}
                      {logsStats && (
                        <div className="flex items-center gap-2">
                          {/* Filters Button */}
                          <button
                            onClick={() => setShowFilterModal(true)}
                            className={`px-3 py-1.5 rounded-xl border transition-colors cursor-pointer flex items-center gap-1.5 ${isCustomRange ? 'bg-amber-50 border-amber-200 hover:bg-amber-100' : 'bg-slate-50 border-slate-200 hover:bg-slate-100'}`}
                            title="Filter by date range"
                          >
                            <Sliders size={12} className={isCustomRange ? 'text-amber-500' : 'text-slate-500'} />
                            <span className={`text-[10px] font-bold uppercase ${isCustomRange ? 'text-amber-600' : 'text-slate-600'}`}>Filters</span>
                          </button>

                          {/* Timezone Toggle */}
                          <button
                            onClick={cycleTimezone}
                            className="px-3 py-1.5 bg-indigo-50 border border-indigo-100 rounded-xl hover:bg-indigo-100 transition-colors cursor-pointer flex items-center gap-1.5"
                            title="Click to cycle timezone"
                          >
                            <Globe size={12} className="text-indigo-500" />
                            <span className="text-[10px] font-bold text-indigo-600 uppercase">{getTimezoneLabel()}</span>
                          </button>
                        </div>
                      )}
                    </div>

                    {/* Enhanced Chart Area */}
                    <div className="h-[320px] w-full relative">
                      {/* GRÁFICO */}
                      {chartData.length > 0 ? (
                        <ResponsiveContainer width="100%" height="100%">
                          <AreaChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 0 }}>
                            <defs>
                              <linearGradient id="colorReq" x1="0" y1="0" x2="0" y2="1">
                                <stop offset="5%" stopColor="#6366f1" stopOpacity={0.4} />
                                <stop offset="50%" stopColor="#6366f1" stopOpacity={0.1} />
                                <stop offset="95%" stopColor="#6366f1" stopOpacity={0} />
                              </linearGradient>
                              <linearGradient id="colorErr" x1="0" y1="0" x2="0" y2="1">
                                <stop offset="5%" stopColor="#334155" stopOpacity={0.5} />
                                <stop offset="95%" stopColor="#334155" stopOpacity={0} />
                              </linearGradient>
                              <linearGradient id="colorSuccess" x1="0" y1="0" x2="0" y2="1">
                                <stop offset="5%" stopColor="#10B981" stopOpacity={0.4} />
                                <stop offset="50%" stopColor="#10B981" stopOpacity={0.1} />
                                <stop offset="95%" stopColor="#10B981" stopOpacity={0} />
                              </linearGradient>
                            </defs>
                            <CartesianGrid strokeDasharray="4 4" vertical={false} stroke="#e2e8f0" />
                            <XAxis
                              dataKey="name"
                              axisLine={false}
                              tickLine={false}
                              tick={{ fontSize: 9, fill: '#64748b', fontWeight: 600 }}
                              minTickGap={10}
                              interval="preserveStartEnd"
                              dy={10}
                              angle={chartData.length > 24 ? 45 : 0}
                              textAnchor={chartData.length > 24 ? "start" : "middle"}
                              height={chartData.length > 24 ? 50 : 30}
                            />
                            <YAxis
                              yAxisId="left"
                              axisLine={false}
                              tickLine={false}
                              tick={{ fontSize: 10, fill: '#64748b', fontWeight: 600 }}
                            />
                            <YAxis
                              yAxisId="right"
                              orientation="right"
                              axisLine={false}
                              tickLine={false}
                              tick={{ fontSize: 9, fill: '#334155', fontWeight: 600 }}
                              domain={[0, Math.max(maxErrorValue * 1.2, 1)]}
                              hide={maxErrorValue <= 1}
                            />
                            <Tooltip
                              content={({ active, payload, label }: { active?: boolean; payload?: Array<{ color: string; name: string; value?: number }>; label?: string }) => {
                                if (!active || !payload) return null;
                                return (
                                  <div className="bg-white/95 backdrop-blur-sm border border-slate-200 rounded-2xl p-4 shadow-2xl">
                                    <p className="text-xs font-bold text-slate-400 uppercase tracking-wider mb-2">{label}</p>
                                    {payload.map((p: { color: string; name: string; value?: number }, i: number) => (
                                      <div key={i} className="flex items-center gap-3 mb-1">
                                        <div
                                          className="w-3 h-3 rounded-full"
                                          style={{ backgroundColor: p.color }}
                                        />
                                        <span className="text-sm font-bold text-slate-700">{p.name}:</span>
                                        <span className="text-sm font-black" style={{ color: p.color }}>{p.value?.toLocaleString()}</span>
                                      </div>
                                    ))}
                                  </div>
                                );
                              }}
                              cursor={{ stroke: '#cbd5e1', strokeWidth: 1, strokeDasharray: '4 4' }}
                            />
                            <Area
                              yAxisId="left"
                              type="monotone"
                              dataKey="requests"
                              stroke="#6366f1"
                              strokeWidth={3}
                              fillOpacity={1}
                              fill="url(#colorReq)"
                              name="Total Requests"
                              animationDuration={1000}
                            />
                            <Area
                              yAxisId="left"
                              type="monotone"
                              dataKey="success"
                              stroke="#10B981"
                              strokeWidth={3}
                              fillOpacity={1}
                              fill="url(#colorSuccess)"
                              name="Success"
                              animationDuration={1200}
                            />
                            <Area
                              yAxisId="right"
                              type="monotone"
                              dataKey="error"
                              stroke="#334155"
                              strokeWidth={maxErrorValue > 0 ? 3 : 0}
                              fillOpacity={1}
                              fill="url(#colorErr)"
                              name="Errors"
                              animationDuration={1400}
                            />
                          </AreaChart>
                        </ResponsiveContainer>
                      ) : logsStats ? (
                        /* Sem chart data mas com logsStats - mostra mensagem */
                        <div className="flex h-full items-center justify-center">
                          <div className="text-center">
                            <Activity size={48} className="mx-auto mb-4 text-slate-300" />
                            <span className="text-sm font-bold text-slate-500">Collecting hourly data...</span>
                          </div>
                        </div>
                      ) : (
                        <div className="flex h-full flex-col items-center justify-center text-slate-300">
                          <div className="relative mb-4">
                            <Wifi size={48} className="opacity-20" />
                            <div className="absolute inset-0 flex items-center justify-center">
                              <Loader2 size={20} className="text-slate-400 animate-spin" />
                            </div>
                          </div>
                          <span className="text-xs font-black uppercase tracking-widest">Syncing with Log Stream...</span>
                          <span className="text-[10px] text-slate-400 mt-2">Analyzing {logsStats?.time_range_hours || 24}h of traffic data</span>
                        </div>
                      )}
                    </div>

                    {/* Bottom: Method Distribution Micro-chart */}
                    {logsStats && logsStats.requests_by_method.length > 0 && (
                      <div className="mt-6 pt-6 border-t border-slate-100">
                        <div className="flex items-center gap-4">
                          <span className="text-[10px] font-bold text-slate-400 uppercase tracking-wider">HTTP Methods</span>
                          <div className="flex-1 flex items-center gap-1">
                            {logsStats.requests_by_method.slice(0, 6).map((m: { method: string; count: number }, i: number) => {
                              const colors = ['bg-blue-600', 'bg-emerald-500', 'bg-amber-500', 'bg-slate-600', 'bg-blue-400', 'bg-blue-500'];
                              const maxCount = logsStats.requests_by_method[0].count;
                              const width = maxCount > 0 ? (m.count / maxCount) * 100 : 0;
                              return (
                                <div key={m.method} className="group relative flex-1">
                                  <div
                                    className={`${colors[i % colors.length]} h-2 rounded-full transition-all duration-500`}
                                    style={{ width: `${Math.max(width, 10)}%` }}
                                  />
                                  <div className="absolute bottom-full mb-1 left-1/2 -translate-x-1/2 opacity-0 group-hover:opacity-100 transition-opacity">
                                    <div className="bg-slate-800 text-white text-[9px] font-bold px-2 py-1 rounded whitespace-nowrap">
                                      {m.method}: {m.count.toLocaleString()}
                                    </div>
                                  </div>
                                </div>
                              );
                            })}
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                </div>

                {/* Health Monitor - Abaixo do Traffic Pulse, layout 3 colunas */}
                <div className="bg-white border border-slate-200 rounded-[2.5rem] p-6 shadow-sm">
                  <div className="grid grid-cols-3 gap-6 items-center">
                    {/* Coluna 1: Título */}
                    <div className="flex items-center gap-3">
                      <Radio size={20} className="text-emerald-500 animate-pulse" />
                      <h3 className="text-lg font-bold text-slate-900">Health Monitor</h3>
                    </div>

                    {/* Coluna 2: Success */}
                    <div className="flex items-center gap-3">
                      <div className="flex-1">
                        <div className="flex items-center justify-between mb-1">
                          <span className="text-xs font-medium text-slate-500">Success (2xx)</span>
                          <span className="text-xs font-bold text-emerald-600">
                            {statusDistribution.length > 0 ? statusDistribution[0].value.toLocaleString() : '0'}
                          </span>
                        </div>
                        <div className="h-3 bg-slate-100 rounded-full overflow-hidden">
                          <div
                            className="h-full bg-emerald-500 rounded-full transition-all duration-500"
                            style={{ width: `${statusDistribution.length > 0 && statusDistribution[0].value + statusDistribution[1].value > 0 ? (statusDistribution[0].value / (statusDistribution[0].value + statusDistribution[1].value)) * 100 : 0}%` }}
                          />
                        </div>
                      </div>
                    </div>

                    {/* Coluna 3: Errors */}
                    <div className="flex items-center gap-3">
                      <div className="flex-1">
                        <div className="flex items-center justify-between mb-1">
                          <span className="text-xs font-medium text-slate-500">Errors (4/5xx)</span>
                          <span className="text-xs font-bold text-slate-700">
                            {statusDistribution.length > 1 ? statusDistribution[1].value.toLocaleString() : '0'}
                          </span>
                        </div>
                        <div className="h-3 bg-slate-100 rounded-full overflow-hidden">
                          <div
                            className="h-full bg-slate-700 rounded-full transition-all duration-500"
                            style={{ width: `${statusDistribution.length > 1 && statusDistribution[0].value + statusDistribution[1].value > 0 ? (statusDistribution[1].value / (statusDistribution[0].value + statusDistribution[1].value)) * 100 : 0}%` }}
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Quick Actions Grid */}
                <div className="hidden max-[900px]:block">
                  <h4 className="text-sm font-semibold text-slate-400 uppercase tracking-widest mb-4 px-2">Quick Navigation</h4>
                  <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                    <QuickAction icon={<Database size={20} />} label="SQL Editor" desc="Execute raw queries" onClick={() => window.location.hash = `#/project/${projectId}/database`} />
                    <QuickAction icon={<Lock size={20} />} label="Security Rules" desc="RLS & Policies" onClick={() => window.location.hash = `#/project/${projectId}/rls`} />
                    <QuickAction icon={<Server size={20} />} label="Storage" desc="Buckets & Assets" onClick={() => window.location.hash = `#/project/${projectId}/storage`} />
                  </div>
                </div>
              </div>

              {/* RIGHT: INFO & ANALYTICS (4 cols) */}
              <div className="lg:col-span-4 space-y-8">

                {/* API ANALYTICS SECTION - Copied from ProjectLogs */}
                {logsStats && (
                  <div className="space-y-6">
                    {/* Stats Header */}

                    {/* Methods Distribution */}
                    <div className="bg-slate-50 border border-slate-200 p-4 rounded-[2rem]">
                      <h5 className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest mb-3">HTTP Methods</h5>
                      <div className="space-y-2">
                        {logsStats.requests_by_method.map((m: { method: string; count: number }) => (
                          <div key={m.method} className="flex items-center gap-3">
                            <span className="text-[10px] font-medium w-12">{m.method}</span>
                            <div className="flex-1 bg-slate-200 rounded-full h-2">
                              <div className="bg-indigo-500 h-2 rounded-full" style={{ width: `${(m.count / logsStats.total_requests * 100) || 0}%` }}></div>
                            </div>
                            <span className="text-[10px] font-bold text-slate-600">{m.count}</span>
                          </div>
                        ))}
                      </div>
                    </div>

                    {/* Top Paths */}
                    <div className="bg-slate-50 border border-slate-200 p-4 rounded-[2rem]">
                      <h5 className="text-[10px] font-semibold text-slate-500 uppercase tracking-widest mb-3">Top Endpoints</h5>
                      <div className="space-y-2 max-h-40 overflow-y-auto">
                        {logsStats.top_paths.slice(0, 5).map((p: { path: string; count: number; avg_duration: number }) => (
                          <div key={p.path} className="flex items-center justify-between text-[10px]">
                            <code className="text-slate-600 truncate max-w-[200px]">{p.path}</code>
                            <div className="flex items-center gap-2">
                              <span className="font-bold text-blue-600">{p.count} reqs</span>
                              <span className="text-slate-400">{p.avg_duration.toFixed(0)}ms</span>
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>

                    {/* Status Distribution */}
                    <div className="grid grid-cols-4 gap-2">
                      {logsStats.status_distribution.slice(0, 8).map((s: { status_code: number; count: number }) => (
                        <div key={s.status_code} className={`p-3 rounded-xl text-center ${s.status_code >= 400 ? 'bg-slate-100 border border-slate-200' : 'bg-emerald-50 border border-emerald-100'}`}>
                          <div className={`text-lg font-bold ${s.status_code >= 400 ? 'text-slate-700' : 'text-emerald-600'}`}>{s.status_code}</div>
                          <div className="text-[9px] text-slate-500">{s.count}</div>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                <div className="hidden max-[900px]:block bg-gradient-to-br from-indigo-900 to-slate-900 text-white rounded-[2.5rem] p-8 relative overflow-hidden shadow-xl">
                  <div className="absolute top-0 right-0 p-6 opacity-10"><Globe size={120} /></div>
                  <div className="relative z-10">
                    <h3 className="text-lg font-bold mb-4 flex items-center gap-2"><Globe size={18} className="text-indigo-400" /> API Endpoint</h3>
                    <div className="bg-white/10 backdrop-blur-md border border-white/10 rounded-xl px-4 py-4 mb-6">
                      <code className="font-mono text-[10px] text-indigo-200 block break-all select-all cursor-text">{getBaseUrl()}</code>
                    </div>
                    <button onClick={() => window.location.hash = `#/project/${projectId}/docs`} className="w-full py-3.5 bg-white text-indigo-900 rounded-xl shadow-lg flex items-center justify-center gap-2 group hover:bg-indigo-50 transition-all text-xs font-semibold uppercase tracking-widest">
                      <BookOpen size={14} className="text-indigo-600" /> Open Documentation
                      <ArrowRight size={14} className="-ml-1 opacity-0 group-hover:opacity-100 group-hover:translate-x-1 transition-all" />
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        )}

        {activeTab === 'intelligence' && <div className="animate-in fade-in slide-in-from-right-4 duration-500"><ProjectIntelligence projectId={projectId} /></div>}
        {activeTab === 'settings' && <div className="animate-in fade-in slide-in-from-right-4 duration-500"><ProjectSettings projectId={projectId} /></div>}
      </div>

      {showDeployModal && (
        <DeployWizard
          projectId={projectId}
          onClose={() => setShowDeployModal(false)}
          onSuccess={() => {
            setShowDeployModal(false);
            fetchProjectData();
          }}
        />
      )}

      {/* Filter Modal - Date Range Picker */}
      {showFilterModal && (
        <div
          className="fixed inset-0 bg-black/20 backdrop-blur-sm z-50 flex items-start justify-center pt-32"
          onClick={() => setShowFilterModal(false)}
        >
          <div
            className="bg-white border border-slate-200 rounded-2xl shadow-2xl p-6 w-[360px] animate-in fade-in slide-in-from-top-2 duration-200"
            onClick={(e: React.MouseEvent) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-lg font-bold text-slate-900 flex items-center gap-2">
                <Sliders size={18} className="text-indigo-500" />
                Date Range Filter
              </h3>
              <button
                onClick={() => setShowFilterModal(false)}
                className="text-slate-400 hover:text-slate-600 transition-colors"
              >
                <X size={20} />
              </button>
            </div>

            <div className="space-y-4">
              <div>
                <label className="text-[10px] font-bold uppercase tracking-wider text-slate-500 mb-1.5 block">
                  Start Date
                </label>
                <input
                  type="datetime-local"
                  value={dateRange.start}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setDateRange((prev: { start: string; end: string }) => ({ ...prev, start: e.target.value }))}
                  className="w-full px-3 py-2 bg-slate-50 border border-slate-200 rounded-xl text-sm font-medium text-slate-700 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500"
                />
              </div>

              <div>
                <label className="text-[10px] font-bold uppercase tracking-wider text-slate-500 mb-1.5 block">
                  End Date
                </label>
                <input
                  type="datetime-local"
                  value={dateRange.end}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setDateRange((prev: { start: string; end: string }) => ({ ...prev, end: e.target.value }))}
                  className="w-full px-3 py-2 bg-slate-50 border border-slate-200 rounded-xl text-sm font-medium text-slate-700 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500"
                />
              </div>

              <div className="pt-2 flex gap-2">
                <button
                  onClick={resetDateFilter}
                  className="flex-1 px-4 py-2.5 bg-slate-100 text-slate-600 rounded-xl text-xs font-bold uppercase tracking-wider hover:bg-slate-200 transition-colors"
                >
                  Reset (24h)
                </button>
                <button
                  onClick={applyDateFilter}
                  className="flex-1 px-4 py-2.5 bg-indigo-600 text-white rounded-xl text-xs font-bold uppercase tracking-wider hover:bg-indigo-700 transition-colors shadow-lg"
                >
                  Apply Filter
                </button>
              </div>
            </div>

            <p className="text-[10px] text-slate-400 mt-4 text-center">
              Graph will use 30min intervals
            </p>
          </div>
        </div>
      )}
    </div>
  );
};

export default ProjectDetail;
