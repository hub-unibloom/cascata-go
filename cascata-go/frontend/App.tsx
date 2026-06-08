import React, { useState, useEffect } from 'react';
import {
  Database, Settings, Shield, Activity, Code2, Users, Layers,
  Plus, Search, Terminal, Server, Key, LogOut, Clock, Settings2, HardDrive, Zap, BookOpen,
  Pin, PinOff, Smartphone, GitBranch, Rocket, Loader2, RefreshCw, Trash2, Sliders, GitPullRequest, Play, RefreshCcw, ChevronRight, X, ArrowLeft, FolderOpen, Palette, Grid3X3
} from 'lucide-react';

// Funções utilitárias inline para leitura da branch via URL
const getCurrentBranchFromURL = () => {
  const hash = window.location.hash || '';
  const match = hash.match(/\/branch\/([^/?]+)/);
  return (match && match[1]) ? match[1] : 'live';
};

// Interceptor global do fetch para garantir cabeçalho x-cascata-env dinâmico baseado na URL do Dashboard (URL-First)
if (typeof window !== 'undefined') {
  const originalFetch = window.fetch;
  window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
    const urlStr = typeof input === 'string' ? input : (input instanceof URL ? input.toString() : (input as any).url || '');
    if (urlStr.includes('/api/')) {
      const hash = window.location.hash || '';
      const match = hash.match(/\/branch\/([^/?]+)/);
      const activeBranch = (match && match[1]) ? match[1] : 'live';

      if (activeBranch && activeBranch !== 'live') {
        init = init || {};
        init.headers = init.headers || {};
        if (init.headers instanceof Headers) {
          init.headers.set('X-Cascata-Env', activeBranch);
        } else if (Array.isArray(init.headers)) {
          const hasEnvHeader = init.headers.some(([k]) => k.toLowerCase() === 'x-cascata-env');
          if (!hasEnvHeader) {
            init.headers.push(['X-Cascata-Env', activeBranch]);
          }
        } else {
          const headersObj = init.headers as Record<string, string>;
          if (!headersObj['X-Cascata-Env'] && !headersObj['x-cascata-env']) {
            headersObj['X-Cascata-Env'] = activeBranch;
          }
        }
      }
    }
    return originalFetch.call(this, input, init);
  };
}

const navigateWithBranch = (hash: string) => {
  const currentBranch = getCurrentBranchFromURL();
  if (currentBranch === 'live' || hash.includes('/branch/')) {
    window.location.hash = hash;
    return;
  }
  // Preserva a branch atual nas navegações internas do dashboard
  const parts = hash.split('/');
  if (parts.length >= 3 && parts[1] === 'project') {
    const projectId = parts[2];
    const section = parts[3] || 'overview';
    const rest = parts.slice(4).join('/');
    window.location.hash = `#/project/${projectId}/${section}/branch/${currentBranch}${rest ? '/' + rest : ''}`;
  } else {
    window.location.hash = hash;
  }
};

import Dashboard from './pages/Dashboard';
import ProjectDetail from './pages/ProjectDetail';
import DatabaseExplorer from './pages/DatabaseExplorer';
import AuthConfig from './pages/AuthConfig';
import RLSManager from './pages/RLSManager';
import RPCManager from './pages/RPCManager';
import Login from './pages/Login';
import SystemSettings from './pages/SystemSettings';
import StorageExplorer from './pages/StorageExplorer';
import EventManager from './pages/EventManager';
import ProjectLogs from './pages/ProjectLogs';
import RLSDesigner from './pages/RLSDesigner';
import APIDocs from './pages/APIDocs';
import PushManager from './pages/PushManager';
import ProjectBackups from './pages/ProjectBackups';
import CascataArchitect from './components/CascataArchitect';
import DeployWizard from './components/deploy/DeployWizard'; // NEW IMPORT
import { BranchManagerModal } from './components/BranchManagerModal';
import AppsPage from './pages/AppsPage';
import Pages from './pages/Pages';


interface BranchRecord {
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

// App Main Entry Point

const App: React.FC = () => {
  const [currentHash, setCurrentHash] = useState(window.location.hash || '#/projects');
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(null);
  const [isAuthenticated, setIsAuthenticated] = useState<boolean>(!!localStorage.getItem('cascata_token'));

  // --- ENVIRONMENT STATE (Branch Context) ---
  // 'live' = banco principal, qualquer outro valor = branch materializada
  // A fonte da verdade é SEMPRE a URL, nunca localStorage
  const [currentEnv, setCurrentEnv] = useState<string>(() => {
    return getCurrentBranchFromURL();
  });

  // DEPLOY STATE (Now Managed by Wizard)
  const [showDeployWizard, setShowDeployWizard] = useState(false);
  const [showBranchManager, setShowBranchManager] = useState(false);
  const [branches, setBranches] = useState<BranchRecord[]>([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [branchFormName, setBranchFormName] = useState('');
  const [branchError, setBranchError] = useState<string | null>(null);
  
  // NEW BRANCH TAB & MERGE HISTORY STATES
  const [branchTab, setBranchTab] = useState<'branches' | 'history'>('branches');
  const [branchParentName, setBranchParentName] = useState('main');
  const [deploysHistory, setDeploysHistory] = useState<any[]>([]);
  const [deploysLoading, setDeploysLoading] = useState(false);
  const [restoringId, setRestoringId] = useState<string | null>(null);

  // DATA BRANCH FORM STATES
  const [dataFormName, setDataFormName] = useState('');
  const [dataFormMode, setDataFormMode] = useState<'copy' | 'reflective' | 'schema_only'>('copy');
  const [dataFormTTL, setDataFormTTL] = useState(168);

  const [envLoading, setEnvLoading] = useState(false);

  // --- QUICK-PEEK OVERLAY (Right-click sidebar → overlay page) ---
  const [quickPeekRoute, setQuickPeekRoute] = useState<string | null>(null);

  // --- SIDEBAR STATE ---
  const [isSidebarLocked, setIsSidebarLocked] = useState<boolean>(() => {
    return localStorage.getItem('cascata_sidebar_locked') !== 'false';
  });
  const [isSidebarHovered, setIsSidebarHovered] = useState(false);

  // --- SIDEBAR ITEM ORDER (Drag-and-Drop Persistent) ---
  const DEFAULT_SIDEBAR_ORDER = [
    'overview', 'database', 'rpc', 'auth', 'rls', 'storage', 'events', 'apps', 'pages', 'push', 'backups', 'docs'
  ];
  const [sidebarOrder, setSidebarOrder] = useState<string[]>(() => {
    try {
      const saved = localStorage.getItem('cascata_sidebar_order');
      if (saved) {
        const parsed = JSON.parse(saved);
        // If 'pages' is not in the saved order, reset to default
        if (!parsed.includes('pages')) {
          localStorage.setItem('cascata_sidebar_order', JSON.stringify(DEFAULT_SIDEBAR_ORDER));
          return DEFAULT_SIDEBAR_ORDER;
        }
        return parsed;
      }
      return DEFAULT_SIDEBAR_ORDER;
    } catch { return DEFAULT_SIDEBAR_ORDER; }
  });
  const [draggedSidebarItem, setDraggedSidebarItem] = useState<string | null>(null);
  const [dragOverSidebarItem, setDragOverSidebarItem] = useState<string | null>(null);

  const handleSidebarReorder = (draggedId: string, targetId: string) => {
    if (draggedId === targetId) return;
    const newOrder = [...sidebarOrder];
    const fromIdx = newOrder.indexOf(draggedId);
    const toIdx = newOrder.indexOf(targetId);
    if (fromIdx === -1 || toIdx === -1) return;
    newOrder.splice(fromIdx, 1);
    newOrder.splice(toIdx, 0, draggedId);
    setSidebarOrder(newOrder);
    localStorage.setItem('cascata_sidebar_order', JSON.stringify(newOrder));
  };

  const isExpanded = isSidebarLocked || isSidebarHovered;

  useEffect(() => {
    localStorage.setItem('cascata_sidebar_locked', String(isSidebarLocked));
  }, [isSidebarLocked]);

  // Remove o efeito que limpava localStorage - não usamos mais para branch

  // --- QUICK-PEEK: Escape key to dismiss ---
  useEffect(() => {
    const handleEsc = (e: KeyboardEvent) => { if (e.key === 'Escape' && quickPeekRoute) setQuickPeekRoute(null); };
    window.addEventListener('keydown', handleEsc);
    return () => window.removeEventListener('keydown', handleEsc);
  }, [quickPeekRoute]);

  // --- BRANCH MANAGER: Escape key to dismiss ---
  useEffect(() => {
    const handleEsc = (e: KeyboardEvent) => { if (e.key === 'Escape' && showBranchManager) setShowBranchManager(false); };
    window.addEventListener('keydown', handleEsc);
    return () => window.removeEventListener('keydown', handleEsc);
  }, [showBranchManager]);

  // --- URL HASH CHANGE HANDLER (Fonte única da verdade) ---
  useEffect(() => {
    const handleHashChange = () => {
      const hash = window.location.hash || '#/projects';
      setCurrentHash(hash);

      // Extrai branch da URL usando a função utilitária
      const detectedEnv = getCurrentBranchFromURL();

      // Extrai projectId da URL
      const parts = hash.split('/');
      let projectId = '';
      if (parts[1] === 'project' && parts[2]) {
        projectId = parts[2];
      }

      setSelectedProjectId(projectId || null);

      // Atualiza estado do ambiente baseado APENAS na URL
      if (detectedEnv !== currentEnv) {
        setCurrentEnv(detectedEnv);
        // Não sincroniza com localStorage - URL é a fonte da verdade
      }
    };
    window.addEventListener('hashchange', handleHashChange);
    handleHashChange();
    return () => window.removeEventListener('hashchange', handleHashChange);
  }, [currentEnv]);

  // --- NAVIGATE FUNCTION (Preserva branch na URL) ---
  const navigate = (hash: string) => {
    // Usa a função utilitária para preservar o branch atual na URL
    navigateWithBranch(hash);
  };
  const handleLogout = () => { localStorage.removeItem('cascata_token'); setIsAuthenticated(false); navigate('#/login'); };

  // --- ENVIRONMENT LOGIC ---
  const handleEnvSwitchClick = async () => {
    if (selectedProjectId) {
      setShowDeployWizard(true);
    }
  };

  const loadBranches = async (projectId: string) => {
    setBranchesLoading(true);
    setBranchError(null);
    try {
      const res = await fetch(`/api/data/${projectId}/branch/list`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data?.error || 'Failed to load branches.');
      setBranches(Array.isArray(data?.branches) ? data.branches : []);
    } catch (e: any) {
      setBranchError(e?.message || 'Failed to load branches.');
    } finally {
      setBranchesLoading(false);
    }
  };

  const loadDeploysHistory = async (projectId: string) => {
    setDeploysLoading(true);
    try {
      const res = await fetch(`/api/data/${projectId}/deploys`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      const data = await res.json();
      if (res.ok && Array.isArray(data)) {
        setDeploysHistory(data);
      }
    } catch (e) {
      console.error('Failed to load deploys history:', e);
    } finally {
      setDeploysLoading(false);
    }
  };

  const handleRestoreDeploy = async (deployId: string) => {
    if (!selectedProjectId) return;
    if (!window.confirm("Você tem certeza absoluta que deseja restaurar o banco de dados principal do inquilino para este checkpoint físico anterior? Isso substituirá todo o estado e dados atuais pelas tabelas preservadas no snapshot!")) return;

    setRestoringId(deployId);
    setBranchError(null);
    try {
      const res = await fetch(`/api/data/${selectedProjectId}/deploys/restore?id=${deployId}`, {
        method: 'POST',
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data?.error || 'Failed to restore database to this checkpoint.');
      
      alert(data.message || 'Restaurado com sucesso!');
      await loadDeploysHistory(selectedProjectId);
    } catch (e: any) {
      setBranchError(e?.message || 'Failed to restore checkpoint.');
    } finally {
      setRestoringId(null);
    }
  };

  const handleCreateBranchFromSnapshot = async (snapshotName: string) => {
    if (!selectedProjectId) return;
    const branchName = window.prompt("Digite o nome da nova Branch de Dados para abrir este Checkpoint:");
    if (!branchName || !branchName.trim()) return;

    setBranchesLoading(true);
    setBranchError(null);
    try {
      const res = await fetch(`/api/data/${selectedProjectId}/branch/create`, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          name: branchName.trim(),
          branch_type: 'data',
          parent_branch: 'main',
          source_snapshot: snapshotName
        })
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || 'Failed to open checkpoint as branch.');
      
      alert(`Branch "${branchName}" criada com sucesso a partir do snapshot físico!`);
      setBranchTab('branches');
      await loadBranches(selectedProjectId);
    } catch (e: any) {
      setBranchError(e?.message || 'Failed to open checkpoint as branch.');
    } finally {
      setBranchesLoading(false);
    }
  };

  const openBranchManager = async () => {
    if (!selectedProjectId) return;
    setBranchTab('branches');
    setBranchParentName('main');
    setShowBranchManager(true);
    await loadBranches(selectedProjectId);
  };

  const handleCreateBranch = async () => {
    if (!selectedProjectId || !branchFormName.trim()) return;
    setBranchesLoading(true);
    setBranchError(null);
    try {
      const res = await fetch(`/api/data/${selectedProjectId}/branch/create`, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          name: branchFormName.trim(),
          branch_type: 'environment',
          parent_branch: branchParentName
        })
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || 'Failed to create branch.');
      setBranchFormName('');
      await loadBranches(selectedProjectId);
    } catch (e: any) {
      setBranchError(e?.message || 'Failed to create branch.');
      setBranchesLoading(false);
    }
  };

  const handleCreateDataBranch = async () => {
    if (!selectedProjectId || !dataFormName.trim()) return;
    setBranchesLoading(true);
    setBranchError(null);
    try {
      const res = await fetch(`/api/data/${selectedProjectId}/branch/create`, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          name: dataFormName.trim(),
          branch_type: 'data',
          parent_branch: 'main',
          data_mode: dataFormMode,
          data_branch_ttl_hours: dataFormTTL
        })
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || 'Failed to create data branch.');
      setDataFormName('');
      setDataFormMode('copy');
      await loadBranches(selectedProjectId);
    } catch (e: any) {
      setBranchError(e?.message || 'Failed to create data branch.');
      setBranchesLoading(false);
    }
  };

  const handleDeleteBranch = async (branchName: string) => {
    if (!selectedProjectId || !confirm(`Delete branch "${branchName}"?`)) return;
    setBranchesLoading(true);
    setBranchError(null);
    try {
      const res = await fetch(`/api/data/${selectedProjectId}/branch/delete?name=${encodeURIComponent(branchName)}`, {
        method: 'DELETE',
        headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error(data?.error || 'Failed to delete branch.');
      await loadBranches(selectedProjectId);
    } catch (e: any) {
      setBranchError(e?.message || 'Failed to delete branch.');
      setBranchesLoading(false);
    }
  };

  const renderContent = () => {
    if (currentHash === '#/login') return <Login onLoginSuccess={() => setIsAuthenticated(true)} />;
    if (!isAuthenticated) return <Login onLoginSuccess={() => setIsAuthenticated(true)} />;
    if (currentHash === '#/projects' || currentHash === '') return <Dashboard onSelectProject={(id) => navigate(`#/project/${id}`)} />;
    if (currentHash === '#/settings') return <SystemSettings />;

    if (currentHash.startsWith('#/project/')) {
      const parts = currentHash.split('/');
      const projectId = parts[2];
      const section = parts[3] || 'overview';

      if (section === 'rls-editor') {
        const entityType = parts[4] as 'table' | 'bucket';
        const entityName = parts[5];
        return <RLSDesigner projectId={projectId} entityType={entityType} entityName={entityName} onBack={() => navigate(`#/project/${projectId}/rls`)} />;
      }
      const key = `${projectId}-${currentEnv}`;

      switch (section) {
        case 'overview': return <ProjectDetail key={key} projectId={projectId} />;
        case 'database': return <DatabaseExplorer key={key} projectId={projectId} />;
        case 'auth': return <AuthConfig key={key} projectId={projectId} currentEnv={currentEnv} />;
        case 'rls': return <RLSManager key={key} projectId={projectId} />;
        case 'rpc': return <RPCManager key={key} projectId={projectId} currentEnv={currentEnv} />;
        case 'storage': return <StorageExplorer key={key} projectId={projectId} />;
        case 'events': return <EventManager key={key} projectId={projectId} />;
        case 'apps': return <AppsPage key={key} projectId={projectId} />; 
        case 'pages': return <Pages key={key} projectId={projectId} />;
        case 'push': return <PushManager key={key} projectId={projectId} />;
        case 'logs': return <ProjectLogs key={key} projectId={projectId} />;
        case 'docs': return <APIDocs key={key} projectId={projectId} currentEnv={currentEnv} />;
        case 'backups': return <ProjectBackups key={key} projectId={projectId} />;
        default: return <ProjectDetail key={key} projectId={projectId} />;
      }
    }
    return <Dashboard onSelectProject={(id) => navigate(`#/project/${id}`)} />;
  };

  if (currentHash === '#/login' || !isAuthenticated) return renderContent();

  const isImmersive = currentHash.includes('/rls-editor');

  return (
    <div className="flex h-screen bg-[#F8FAFC] overflow-hidden">
      {!isImmersive && (
        <>
          {/* SIDEBAR CONTAINER */}
          <aside
            className={`
              fixed top-0 left-0 h-full bg-white border-r border-slate-200 shadow-xl z-50 
              transition-all duration-300 ease-in-out flex flex-col
              ${isExpanded ? 'w-[260px]' : 'w-[88px]'}
            `}
            onMouseEnter={() => setIsSidebarHovered(true)}
            onMouseLeave={() => setIsSidebarHovered(false)}
          >
            {/* HEADER */}
            <div className={`p-5 flex items-center ${isExpanded ? 'justify-between' : 'justify-center'} border-b border-slate-100 transition-all duration-300`}>
              {isExpanded ? (
                <div className="flex items-center gap-3 animate-in fade-in duration-300">
                  <div className="w-9 h-9 bg-indigo-600 rounded-xl flex items-center justify-center shadow-lg shadow-indigo-200 shrink-0">
                    <Layers className="text-white w-5 h-5" />
                  </div>
                  <div>
                    <span className="font-bold text-lg tracking-tight text-slate-900 block leading-none">Cascata</span>
                    <span className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-1 block">Studio v1.0</span>
                  </div>
                </div>
              ) : (
                <div className="w-12 h-12 bg-indigo-600 rounded-2xl flex items-center justify-center shadow-lg shadow-indigo-200 shrink-0 mb-2">
                  <Layers className="text-white w-7 h-7" />
                </div>
              )}

              {isExpanded && (
                <button
                  onClick={() => setIsSidebarLocked(!isSidebarLocked)}
                  className={`p-1.5 rounded-lg transition-colors ${isSidebarLocked ? 'text-indigo-600 bg-indigo-50' : 'text-slate-400 hover:text-slate-600 hover:bg-slate-100'}`}
                  title={isSidebarLocked ? "Destravar Menu" : "Travar Menu"}
                >
                  {isSidebarLocked ? <Pin size={16} className="fill-current" /> : <PinOff size={16} />}
                </button>
              )}
            </div>

            {/* NAV CONTENT */}
            <nav className="flex-1 p-3 space-y-2 overflow-y-auto overflow-x-hidden custom-scrollbar">
              {selectedProjectId && (
                <>
                  {isExpanded && <div className="text-[10px] font-bold text-slate-400 uppercase tracking-[0.2em] mb-2 px-3 mt-2 animate-in fade-in">Instance</div>}

                  {/* DRAGGABLE SIDEBAR ITEMS — order persisted in localStorage */}
                  {sidebarOrder.map((itemId) => {
                    const SIDEBAR_ITEMS: Record<string, { icon: React.ReactElement; label: string; route: string; matchKey: string }> = {
                      overview: { icon: <Activity />, label: 'Overview', route: 'overview', matchKey: '/overview' },
                      database: { icon: <Database />, label: 'Data Browser', route: 'database', matchKey: '/database' },
                      rpc: { icon: <Clock />, label: 'RPC & Logic', route: 'rpc', matchKey: '/rpc' },
                      auth: { icon: <Users />, label: 'Auth Services', route: 'auth', matchKey: '/auth' },
                      rls: { icon: <Shield />, label: 'Access Control', route: 'rls', matchKey: '/rls' },
                      storage: { icon: <HardDrive />, label: 'Native Storage', route: 'storage', matchKey: '/storage' },
                      events: { icon: <Zap />, label: 'Event Hooks', route: 'events', matchKey: '/events' },
                      apps: { icon: <Grid3X3 />, label: 'Apps', route: 'apps', matchKey: '/apps' },
                      pages: { icon: <Palette />, label: 'Pages', route: 'pages', matchKey: '/pages' },
                      push: { icon: <Smartphone />, label: 'Push Engine', route: 'push', matchKey: '/push' },
                      backups: { icon: <Settings />, label: 'Backups', route: 'backups', matchKey: '/backups' },
                      docs: { icon: <BookOpen />, label: 'API Docs', route: 'docs', matchKey: '/docs' },
                    };
                    const item = SIDEBAR_ITEMS[itemId];
                    if (!item) return null;
                    return (
                      <div
                        key={itemId}
                        draggable
                        onDragStart={(e) => { e.dataTransfer.setData('text/sidebar', itemId); setDraggedSidebarItem(itemId); }}
                        onDragEnd={() => { setDraggedSidebarItem(null); setDragOverSidebarItem(null); }}
                        onDragOver={(e) => { e.preventDefault(); setDragOverSidebarItem(itemId); }}
                        onDrop={(e) => {
                          e.preventDefault();
                          const from = e.dataTransfer.getData('text/sidebar');
                          if (from) handleSidebarReorder(from, itemId);
                          setDragOverSidebarItem(null);
                        }}
                        className={`transition-all ${dragOverSidebarItem === itemId ? 'border-t-2 border-indigo-400' : ''} ${draggedSidebarItem === itemId ? 'opacity-40' : ''}`}
                      >
                        <SidebarItem
                          icon={item.icon}
                          label={item.label}
                          active={currentHash.includes(item.matchKey)}
                          expanded={isExpanded}
                          onClick={() => navigate(`#/project/${selectedProjectId}/${item.route}`)}
                          onContextMenu={(e) => { e.preventDefault(); setQuickPeekRoute(item.route); }}
                        />
                      </div>
                    );
                  })}

                  <div className={`my-4 h-[1px] bg-slate-100 ${isExpanded ? 'mx-3' : 'mx-1'}`}></div>
                </>
              )}
            </nav>

            {/* ENVIRONMENT SWITCHER WIDGET */}
            {selectedProjectId && (
              <div className="px-3 pb-2 animate-in fade-in slide-in-from-bottom-2">
                <div
                  className={`
                    rounded-xl border transition-all duration-300 relative overflow-hidden
                    ${currentEnv === 'live' ? 'bg-emerald-50/50 border-emerald-100' : 'bg-amber-50/50 border-amber-100'}
                    ${isExpanded ? 'p-3' : 'p-2 flex flex-col items-center gap-2'}
                  `}
                >
                  <div className={`flex items-center ${isExpanded ? 'justify-between' : 'justify-center'} w-full`}>
                    {isExpanded ? (
                      <div className="flex items-center gap-2">
                        <div className={`w-2 h-2 rounded-full ${currentEnv === 'live' ? 'bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]' : 'bg-amber-500 shadow-[0_0_8px_rgba(245,158,11,0.5)]'}`}></div>
                        <span className={`text-xs font-black uppercase tracking-widest ${currentEnv === 'live' ? 'text-emerald-800' : 'text-amber-800'}`}>
                          {currentEnv}
                        </span>
                      </div>
                    ) : (
                      <div
                        className={`w-3 h-3 rounded-full ${currentEnv === 'live' ? 'bg-emerald-500' : 'bg-amber-500'}`}
                        title={`Current Environment: ${currentEnv.toUpperCase()}`}
                      ></div>
                    )}

                    <button
                      onClick={handleEnvSwitchClick}
                      className={`p-1.5 rounded-lg transition-colors ${currentEnv === 'live' ? 'hover:bg-emerald-100 text-emerald-600' : 'hover:bg-amber-100 text-amber-600'}`}
                      title="Open Branch Deploy"
                    >
                      <RefreshCw size={14} className={envLoading ? 'animate-spin' : ''} />
                    </button>
                  </div>

                  {isExpanded && (
                    <div className="flex flex-col gap-2 mt-3">
                      {currentEnv !== 'live' && (
                        <button
                          onClick={() => {
                            // Remove branch da URL para voltar ao live
                            const parts = currentHash.split('/');
                            if (parts.length >= 3 && parts[1] === 'project') {
                              const projectId = parts[2];
                              const section = parts[3] || 'overview';
                              // Pega tudo que estiver depois do nome da branch, se houver
                              let rest = '';
                              const branchIndex = parts.indexOf('branch');
                              if (branchIndex !== -1 && parts.length > branchIndex + 2) {
                                rest = '/' + parts.slice(branchIndex + 2).join('/');
                              }
                              window.location.replace(`#/project/${projectId}/${section}${rest}`);
                            } else {
                              window.location.replace('#/projects');
                            }
                            localStorage.removeItem('cascata_env'); document.cookie = "cascata_active_env=; Path=/; Expires=Thu, 01 Jan 1970 00:00:01 GMT;";
                            setCurrentEnv('live');
                            window.location.reload();
                          }}
                          className="w-full bg-white border-2 border-emerald-500 text-emerald-600 hover:bg-emerald-50 text-[10px] font-black uppercase tracking-widest py-2.5 rounded-xl shadow-sm flex items-center justify-center gap-2 transition-all active:scale-95 mb-1 group"
                        >
                          <ArrowLeft size={12} className="group-hover:-translate-x-1 transition-transform" />
                          Return to Live
                        </button>
                      )}
                      <div className="flex gap-2">
                        <button
                          onClick={openBranchManager}
                          className="flex-1 bg-white border border-slate-200 hover:border-blue-300 hover:bg-blue-50 text-slate-600 text-[10px] font-black uppercase tracking-widest py-2 rounded-lg shadow-sm flex items-center justify-center gap-2 transition-all active:scale-95"
                        >
                          <GitBranch size={12} /> Branches
                        </button>
                        <button
                          onClick={() => setShowDeployWizard(true)}
                          className="flex-1 bg-amber-500 hover:bg-amber-600 text-white text-[10px] font-black uppercase tracking-widest py-2 rounded-lg shadow-sm flex items-center justify-center gap-2 transition-all active:scale-95"
                        >
                          <Rocket size={12} /> Deploy
                        </button>
                      </div>
                    </div>
                  )}

                  {!isExpanded && (
                    <div className="mt-1 flex flex-col items-center gap-1">
                      <button onClick={openBranchManager} className="text-emerald-600 hover:text-emerald-700"><GitBranch size={16} /></button>
                      <button onClick={() => setShowDeployWizard(true)} className="text-amber-500 hover:text-amber-600"><Rocket size={16} /></button>
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* FOOTER NAV */}
            <div className="p-3 pb-4 space-y-2 bg-white border-t border-slate-50">
              {isExpanded && <div className="text-[10px] font-bold text-slate-400 uppercase tracking-[0.2em] mb-2 px-3 animate-in fade-in">Main Console</div>}

              <SidebarItem icon={<Server />} label="All Projects" active={currentHash === '#/projects'} expanded={isExpanded} onClick={() => navigate('#/projects')} />
              <SidebarItem icon={<Settings2 />} label="System Settings" active={currentHash === '#/settings'} expanded={isExpanded} onClick={() => navigate('#/settings')} />

              <div className={`my-2 h-[1px] bg-slate-100 ${isExpanded ? 'mx-0' : 'mx-1'}`}></div>

              <button
                onClick={handleLogout}
                className={`
                  w-full flex items-center rounded-xl transition-all group font-medium border border-transparent
                  ${isExpanded
                    ? 'justify-between px-3 py-2 bg-slate-50 border-slate-200 text-slate-500 hover:text-rose-600 text-xs'
                    : 'justify-center p-3 text-slate-400 hover:bg-rose-50 hover:text-rose-600'}
                `}
                title="Logout"
              >
                <div className="flex items-center gap-2">
                  <LogOut size={isExpanded ? 14 : 20} />
                  {isExpanded && <span>Logout</span>}
                </div>
              </button>
            </div>
          </aside>

          <div className={`shrink-0 transition-all duration-300 ease-in-out ${isSidebarLocked ? 'w-[260px]' : 'w-[88px]'}`} />
        </>
      )}

      <main className="flex-1 overflow-y-auto flex flex-col relative text-slate-900 h-full w-full">
        <div className="flex-1 min-w-0">
          {renderContent()}
        </div>
        {selectedProjectId && <CascataArchitect projectId={selectedProjectId} />}
      </main>


      {/* QUICK-PEEK OVERLAY — Right-click sidebar to open page as overlay */}
      {quickPeekRoute && selectedProjectId && (
        <div
          className="fixed inset-0 z-[800] flex items-center justify-center animate-in fade-in zoom-in-95 duration-200"
          onClick={(e) => { if (e.target === e.currentTarget) setQuickPeekRoute(null); }}
          style={{ background: 'rgba(15, 23, 42, 0.6)', backdropFilter: 'blur(12px)' }}
        >
          <div
            className="bg-white rounded-3xl shadow-2xl overflow-hidden flex flex-col border border-slate-200/50"
            style={{ width: '94vw', height: '94vh' }}
          >
            {/* Header */}
            <div className="h-11 bg-slate-50 border-b border-slate-100 flex items-center justify-between px-5 shrink-0">
              <div className="flex items-center gap-2">
                <div className="w-2 h-2 rounded-full bg-indigo-500 shadow-[0_0_6px_rgba(99,102,241,0.5)]"></div>
                <span className="text-[10px] font-black text-slate-500 uppercase tracking-[0.2em]">Quick Peek</span>
              </div>
              <button onClick={() => setQuickPeekRoute(null)} className="text-slate-400 hover:text-slate-700 transition-colors p-1 hover:bg-slate-100 rounded-lg" title="Close (Esc)">
                <X size={16} />
              </button>
            </div>
            {/* Content — renders the actual page component */}
            <div className="flex-1 overflow-y-auto overflow-x-hidden min-h-0">
              {(() => {
                const key = `peek-${selectedProjectId}-${currentEnv}-${quickPeekRoute}`;
                switch (quickPeekRoute) {
                  case 'overview': return <ProjectDetail key={key} projectId={selectedProjectId} />;
                  case 'database': return <DatabaseExplorer key={key} projectId={selectedProjectId} />;
                  case 'auth': return <AuthConfig key={key} projectId={selectedProjectId} currentEnv={currentEnv} />;
                  case 'rls': return <RLSManager key={key} projectId={selectedProjectId} />;
                  case 'rpc': return <RPCManager key={key} projectId={selectedProjectId} currentEnv={currentEnv} />;
                  case 'storage': return <StorageExplorer key={key} projectId={selectedProjectId} />;
                  case 'events': return <EventManager key={key} projectId={selectedProjectId} />;
                  case 'push': return <PushManager key={key} projectId={selectedProjectId} />;
                  case 'docs': return <APIDocs key={key} projectId={selectedProjectId} currentEnv={currentEnv} />;
                  case 'backups': return <ProjectBackups key={key} projectId={selectedProjectId} />;
                  default: return null;
                }
              })()}
            </div>
          </div>
        </div>
      )}

      {/* NEW DEPLOY WIZARD */}
      {showDeployWizard && selectedProjectId && (
        <DeployWizard
          projectId={selectedProjectId}
          onClose={() => setShowDeployWizard(false)}
          onSuccess={() => {
            const parts = currentHash.split('/');
            if (parts.length >= 3 && parts[1] === 'project') {
              window.location.replace(`#/project/${parts[2]}/${parts[3] || 'overview'}`);
            }
            setCurrentEnv('live');
            window.location.reload();
          }}
        />
      )}

      <BranchManagerModal
        isOpen={showBranchManager}
        onClose={() => setShowBranchManager(false)}
        selectedProjectId={selectedProjectId}
        currentEnv={currentEnv}
        branches={branches}
        branchesLoading={branchesLoading}
        loadBranches={loadBranches}
        handleCreateBranch={handleCreateBranch}
        handleCreateDataBranch={handleCreateDataBranch}
        handleDeleteBranch={handleDeleteBranch}
        handleCreateBranchFromSnapshot={handleCreateBranchFromSnapshot}
        handleRestoreDeploy={handleRestoreDeploy}
        branchFormName={branchFormName}
        setBranchFormName={setBranchFormName}
        branchError={branchError}
        branchTab={branchTab}
        setBranchTab={setBranchTab}
        branchParentName={branchParentName}
        setBranchParentName={setBranchParentName}
        deploysHistory={deploysHistory}
        loadDeploysHistory={loadDeploysHistory}
        deploysLoading={deploysLoading}
        restoringId={restoringId}
        dataFormName={dataFormName}
        setDataFormName={setDataFormName}
        dataFormMode={dataFormMode}
        setDataFormMode={setDataFormMode}
        dataFormTTL={dataFormTTL}
        setDataFormTTL={setDataFormTTL}
        setShowDeployWizard={setShowDeployWizard}
        currentEnvSetter={setCurrentEnv}
        currentHash={currentHash}
      />

    </div>
  );
};

// COMPONENTE DE ITEM INTELIGENTE
const SidebarItem: React.FC<{
  icon: React.ReactElement,
  label: string,
  active: boolean,
  expanded: boolean,
  onClick: () => void,
  onContextMenu?: (e: React.MouseEvent) => void
}> = ({ icon, label, active, expanded, onClick, onContextMenu }) => {
  // Ajuste: 18px expandido, 21px recolhido (+3px apenas — anti pré-cognitivo fix)
  const iconSize = expanded ? 18 : 21;
  const TheIcon = React.cloneElement(icon as React.ReactElement<any>, { size: iconSize });

  return (
    <button
      onClick={onClick}
      onContextMenu={onContextMenu}
      title={!expanded ? label : undefined}
      className={`
        flex items-center transition-all duration-200 rounded-xl group relative
        ${expanded
          ? 'w-full gap-3 px-3 py-2.5 text-sm justify-start'
          : 'w-full justify-center py-4'
        }
        ${active
          ? 'bg-indigo-600 text-white font-semibold shadow-lg shadow-indigo-200'
          : 'text-slate-500 hover:bg-slate-50 hover:text-indigo-600'
        }
      `}
    >
      <span className={`transition-colors ${active ? 'text-white' : 'text-slate-400 group-hover:text-indigo-600'}`}>
        {TheIcon}
      </span>

      {expanded && (
        <span className="truncate animate-in fade-in slide-in-from-left-2 duration-200">
          {label}
        </span>
      )}

      {/* Indicador Ativo no modo recolhido */}
      {!expanded && active && (
        <div className="absolute left-0 top-1/2 -translate-y-1/2 w-1 h-8 bg-indigo-600 rounded-r-full"></div>
      )}
    </button>
  );
};

export default App;