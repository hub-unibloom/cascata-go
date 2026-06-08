import React, { useState } from 'react';
import {
  Plus, Upload, Globe, FolderOpen, Trash2, Edit3, Play, Settings, Search, Filter,
  GitBranch, Code2, Terminal, Database, Shield, Globe2, Zap, Clock, HardDrive,
  BarChart3, Users, Activity, TrendingUp, Eye, Download, Copy, ExternalLink,
  ChevronRight, ChevronDown, CheckCircle, AlertCircle, XCircle, RefreshCw,
  MoreHorizontal, FileCode, Server, Lock, Key, Layers, Cpu,
  Palette, Layout, Monitor, Smartphone, Tablet, Image, File, Folder, Archive,
  ArrowUpRight, ArrowDownRight, ArrowRight, Calendar, Hash, Tag, Box, Link2, Maximize2,
  X, Check, AlertTriangle, Info, ChevronLeft, GripVertical, PlayCircle, PauseCircle
} from 'lucide-react';

interface PagesProps {
  projectId: string;
}

// Color palette from the original design
const colors = {
  primary: 'from-indigo-500 to-purple-600',
  success: 'bg-emerald-100 text-emerald-700 border-emerald-200',
  warning: 'bg-amber-100 text-amber-700 border-amber-200',
  danger: 'bg-red-100 text-red-700 border-red-200',
  info: 'bg-blue-100 text-blue-700 border-blue-200',
};

// Mock data
const mockSites = [
  {
    id: '1',
    name: 'meu-site.com',
    branch: 'main',
    status: 'active',
    lastDeploy: '2 horas atrás',
    visitors: 12450,
    bandwidth: '2.3 GB',
    builds: 47,
    ssl: true,
    domain: 'meu-site.com',
    slug: 'meu-site',
    framework: 'Next.js',
    frameworkIcon: '⚡',
  },
  {
    id: '2',
    name: 'blog-pessoal',
    branch: 'develop',
    status: 'active',
    lastDeploy: '1 dia atrás',
    visitors: 8340,
    bandwidth: '1.8 GB',
    builds: 32,
    ssl: true,
    domain: 'blog-pessoal.vercel.app',
    slug: 'blog-pessoal',
    framework: 'Gatsby',
    frameworkIcon: '🔮',
  },
  {
    id: '3',
    name: 'dashboard-admin',
    branch: 'main',
    status: 'building',
    lastDeploy: 'Em progresso...',
    visitors: 0,
    bandwidth: '0 GB',
    builds: 1,
    ssl: false,
    domain: 'dashboard-admin.vercel.app',
    slug: 'dashboard-admin',
    framework: 'React',
    frameworkIcon: '⚛️',
  },
  {
    id: '4',
    name: 'landing-page',
    branch: 'main',
    status: 'error',
    lastDeploy: '3 dias atrás',
    visitors: 2980,
    bandwidth: '890 MB',
    builds: 18,
    ssl: true,
    domain: 'landing-page.vercel.app',
    slug: 'landing-page',
    framework: 'Vue',
    frameworkIcon: '💚',
  },
];


const mockEnvironments = [
  { name: 'Production', color: 'emerald', varCount: 24 },
  { name: 'Preview', color: 'blue', varCount: 24 },
  { name: 'Development', color: 'amber', varCount: 31 },
];

const mockDeployHistory = [
  { id: 'd1', version: 'v2.4.1', time: '2h ago', status: 'success', duration: '45s', author: 'Maria Silva', message: 'Update landing page' },
  { id: 'd2', version: 'v2.4.0', time: '1d ago', status: 'success', duration: '52s', author: 'João Santos', message: 'Add new features' },
  { id: 'd3', version: 'v2.3.9', time: '2d ago', status: 'failed', duration: '1m 12s', author: 'Maria Silva', message: 'Fix responsive bugs' },
  { id: 'd4', version: 'v2.3.8', time: '3d ago', status: 'success', duration: '48s', author: 'Pedro Costa', message: 'Update dependencies' },
  { id: 'd5', version: 'v2.3.7', time: '5d ago', status: 'success', duration: '55s', author: 'Ana Oliveira', message: 'Refactor components' },
];

const mockTeamMembers = [
  { name: 'Maria Silva', role: 'Admin', avatar: '👩‍💻', lastAccess: 'Agora' },
  { name: 'João Santos', role: 'Developer', avatar: '👨‍💻', lastAccess: '1h atrás' },
  { name: 'Pedro Costa', role: 'Developer', avatar: '👨‍🚀', lastAccess: '3h atrás' },
  { name: 'Ana Oliveira', role: 'Viewer', avatar: '👩‍🎨', lastAccess: '1d atrás' },
];

const mockCDNRules = [
  { path: '/static/*', cache: '1 year', edge: 'Global CDN' },
  { path: '/images/*', cache: '6 months', edge: 'Global CDN' },
  { path: '/api/*', cache: 'no-cache', edge: 'No caching' },
  { path: '/*.json', cache: '1 day', edge: 'Global CDN' },
];

// Components
const StatCard = ({ icon: Icon, label, value, trend, trendDirection }: any) => (
  <div className="bg-white rounded-xl p-5 border border-slate-200 hover:shadow-md transition-all">
    <div className="flex items-center justify-between mb-3">
      <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-indigo-500 to-purple-600 flex items-center justify-center">
        <Icon size={20} className="text-white" />
      </div>
      {trend && (
        <span className={`flex items-center gap-1 text-xs font-medium px-2 py-1 rounded-full ${
          trendDirection === 'up' ? 'bg-emerald-100 text-emerald-700' : 'bg-red-100 text-red-700'
        }`}>
          {trendDirection === 'up' ? <ArrowUpRight size={12} /> : <ArrowDownRight size={12} />}
          {trend}
        </span>
      )}
    </div>
    <p className="text-2xl font-bold text-slate-900 mb-1">{value}</p>
    <p className="text-sm text-slate-500">{label}</p>
  </div>
);

const StatusBadge = ({ status }: { status: string }) => {
  const styles: Record<string, string> = {
    active: 'bg-emerald-100 text-emerald-700 border border-emerald-200',
    building: 'bg-blue-100 text-blue-700 border border-blue-200',
    error: 'bg-red-100 text-red-700 border border-red-200',
    inactive: 'bg-slate-100 text-slate-600 border border-slate-200',
    pending: 'bg-amber-100 text-amber-700 border border-amber-200',
    deprecated: 'bg-slate-100 text-slate-500 border border-slate-200',
    success: 'bg-emerald-100 text-emerald-700 border border-emerald-200',
    failed: 'bg-red-100 text-red-700 border border-red-200',
  };
  
  return (
    <span className={`px-3 py-1 rounded-full text-xs font-medium ${styles[status] || styles.inactive}`}>
      {status === 'active' && <CheckCircle size={12} className="inline mr-1" />}
      {status === 'building' && <RefreshCw size={12} className="inline mr-1 animate-spin" />}
      {status === 'error' && <XCircle size={12} className="inline mr-1" />}
      {status === 'pending' && <Clock size={12} className="inline mr-1" />}
      {status === 'failed' && <XCircle size={12} className="inline mr-1" />}
      {status === 'success' && <CheckCircle size={12} className="inline mr-1" />}
      {status.charAt(0).toUpperCase() + status.slice(1)}
    </span>
  );
};

const TabButton = ({ active, icon: Icon, label, count, onClick }: any) => (
  <button
    onClick={onClick}
    className={`flex items-center gap-2 px-4 py-2.5 rounded-lg font-medium text-sm transition-all ${
      active 
        ? 'bg-gradient-to-r from-indigo-500 to-purple-600 text-white shadow-lg shadow-indigo-500/30' 
        : 'text-slate-600 hover:bg-slate-100'
    }`}
  >
    <Icon size={16} />
    {label}
    {count !== undefined && (
      <span className={`px-2 py-0.5 rounded-full text-xs ${
        active ? 'bg-white/20' : 'bg-slate-200'
      }`}>
        {count}
      </span>
    )}
  </button>
);

const Modal = ({ isOpen, onClose, title, children, size = 'md' }: any) => {
  if (!isOpen) return null;
  
  const sizes: Record<string, string> = {
    sm: 'max-w-md',
    md: 'max-w-2xl',
    lg: 'max-w-4xl',
    xl: 'max-w-6xl',
  };
  
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div className={`relative bg-white rounded-2xl shadow-2xl w-full ${sizes[size]} max-h-[90vh] overflow-hidden`}>
        <div className="flex items-center justify-between p-5 border-b border-slate-200">
          <h2 className="text-xl font-bold text-slate-900">{title}</h2>
          <button onClick={onClose} className="p-2 hover:bg-slate-100 rounded-lg transition-colors">
            <X size={20} className="text-slate-500" />
          </button>
        </div>
        <div className="p-5 overflow-y-auto max-h-[calc(90vh-80px)]">
          {children}
        </div>
      </div>
    </div>
  );
};

const UploadModal = ({ isOpen, onClose, projectId, onUploadSuccess }: any) => {
  const [dragActive, setDragActive] = useState(false);
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [siteName, setSiteName] = useState("");
  const [customDomain, setCustomDomain] = useState("");
  const [isUploading, setIsUploading] = useState(false);
  const [errorMsg, setErrorMsg] = useState("");

  const handleDrag = (e: React.FormEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.type === 'dragenter' || e.type === 'dragover') {
      setDragActive(true);
    } else if (e.type === 'dragleave') {
      setDragActive(false);
    }
  };
  
  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragActive(false);
    if (e.dataTransfer.files && e.dataTransfer.files[0]) {
      const file = e.dataTransfer.files[0];
      if (file.name.endsWith('.zip')) {
        setSelectedFile(file);
        setErrorMsg("");
      } else {
        setErrorMsg("Por favor, envie apenas arquivos .zip");
      }
    }
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files[0]) {
      const file = e.target.files[0];
      if (file.name.endsWith('.zip')) {
        setSelectedFile(file);
        setErrorMsg("");
      } else {
        setErrorMsg("Por favor, envie apenas arquivos .zip");
      }
    }
  };

  const handleUpload = async () => {
    if (!selectedFile) {
      setErrorMsg("Selecione um arquivo ZIP.");
      return;
    }
    if (!siteName) {
      setErrorMsg("O nome do site é obrigatório.");
      return;
    }

    setIsUploading(true);
    setErrorMsg("");
    setUploadProgress(5);

    const formData = new FormData();
    formData.append("file", selectedFile);
    formData.append("name", siteName);
    if (customDomain) {
      formData.append("domain", customDomain);
    }

    try {
      const token = localStorage.getItem('cascata_token');
      console.log('Starting upload to:', `/api/control/projects/${projectId}/sites/deploy`);
      console.log('Token exists:', !!token);
      
      const xhr = new XMLHttpRequest();
      xhr.open('POST', `/api/control/projects/${projectId}/sites/deploy`, true);
      xhr.setRequestHeader('Authorization', `Bearer ${token}`);

      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) {
          const percentComplete = Math.round((event.loaded / event.total) * 100);
          setUploadProgress(percentComplete);
          console.log('Upload progress:', percentComplete + '%');
        }
      };

      xhr.onload = () => {
        console.log('XHR onload - Status:', xhr.status, 'Response:', xhr.responseText);
        if (xhr.status >= 200 && xhr.status < 300) {
          setUploadProgress(100);
          // Parse deploy response so we can surface auto-detected active_folder
          let deployResult: any = {};
          try { deployResult = JSON.parse(xhr.responseText); } catch (_) {}
          console.log('Upload successful, calling onUploadSuccess with result:', deployResult);
          setTimeout(() => {
            onUploadSuccess(deployResult);
            onClose();
            setSelectedFile(null);
            setSiteName("");
            setCustomDomain("");
            setUploadProgress(null);
            setIsUploading(false);
          }, 500);
        } else {
          let errText = `Erro no upload (Status: ${xhr.status})`;
          try {
            const res = JSON.parse(xhr.responseText);
            errText = res.error || res.message || errText;
          } catch(e) {
            errText += ' - ' + (xhr.responseText || 'Sem resposta do servidor');
          }
          console.error('Upload error:', errText);
          setErrorMsg(errText);
          setIsUploading(false);
          setUploadProgress(null);
        }
      };

      xhr.onerror = () => {
        console.error('XHR network error');
        setErrorMsg("Erro de rede durante o upload. Verifique sua conexão.");
        setIsUploading(false);
        setUploadProgress(null);
      };

      xhr.ontimeout = () => {
        console.error('XHR timeout');
        setErrorMsg("Timeout: O servidor demorou muito para responder.");
        setIsUploading(false);
        setUploadProgress(null);
      };

      xhr.timeout = 300000; // 5 minutes timeout
      xhr.send(formData);

    } catch (err: any) {
      console.error('Upload exception:', err);
      setIsUploading(false);
      setUploadProgress(null);
      setErrorMsg(err.message || "Falha no deploy do site");
    }
  };

  return (
    <Modal isOpen={isOpen} onClose={onClose} title="Deploy de Site Estático" size="xl">
      <div className="space-y-6">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-slate-700 mb-2">Nome do Site *</label>
            <input
              type="text"
              placeholder="Ex: meu-site-lindo"
              value={siteName}
              onChange={(e) => setSiteName(e.target.value)}
              disabled={isUploading}
              className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-slate-700 mb-2">Domínio Customizado (opcional)</label>
            <input
              type="text"
              placeholder="Ex: site.meudominio.com"
              value={customDomain}
              onChange={(e) => setCustomDomain(e.target.value)}
              disabled={isUploading}
              className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
            />
          </div>
        </div>

        <div
          onDragEnter={handleDrag}
          onDragLeave={handleDrag}
          onDragOver={handleDrag}
          onDrop={handleDrop}
          className={`border-2 border-dashed rounded-xl p-12 text-center transition-all ${
            dragActive 
              ? 'border-indigo-500 bg-indigo-50' 
              : 'border-slate-300 hover:border-indigo-400 hover:bg-slate-50'
          }`}
        >
          <div className="w-16 h-16 mx-auto mb-4 bg-gradient-to-br from-indigo-500 to-purple-600 rounded-full flex items-center justify-center">
            <Upload size={32} className="text-white" />
          </div>
          <h3 className="text-lg font-semibold text-slate-900 mb-2">
            Arraste seu arquivo ZIP aqui
          </h3>
          <p className="text-slate-500 mb-4">ou clique para selecionar</p>
          <input 
            type="file" 
            id="file-upload" 
            className="hidden" 
            accept=".zip" 
            onChange={handleFileChange}
            disabled={isUploading}
          />
          <label
            htmlFor="file-upload"
            className="inline-flex items-center gap-2 px-6 py-3 bg-gradient-to-r from-indigo-500 to-purple-600 text-white rounded-lg font-medium cursor-pointer hover:shadow-lg hover:shadow-indigo-500/30 transition-all"
          >
            <FolderOpen size={18} />
            Selecionar ZIP
          </label>
          {selectedFile && (
            <p className="mt-4 text-emerald-600 font-medium">
              Arquivo selecionado: {selectedFile.name} ({(selectedFile.size / 1024 / 1024).toFixed(2)} MB)
            </p>
          )}
        </div>

        {errorMsg && (
          <div className="p-3 bg-red-50 text-red-700 rounded-lg border border-red-200 text-sm">
            {errorMsg}
          </div>
        )}

        {uploadProgress !== null ? (
          <div className="space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="text-slate-600">Enviando site...</span>
              <span className="font-medium text-indigo-600">{uploadProgress}%</span>
            </div>
            <div className="h-2 bg-slate-200 rounded-full overflow-hidden">
              <div 
                className="h-full bg-gradient-to-r from-indigo-500 to-purple-600 transition-all duration-300"
                style={{ width: `${uploadProgress}%` }}
              />
            </div>
          </div>
        ) : (
          <div className="flex gap-3">
            <button 
              onClick={onClose}
              disabled={isUploading}
              className="flex-1 py-3 bg-slate-100 text-slate-700 rounded-lg font-medium hover:bg-slate-200 transition-colors"
            >
              Cancelar
            </button>
            <button 
              onClick={handleUpload}
              disabled={isUploading}
              className="flex-1 py-3 bg-gradient-to-r from-indigo-500 to-purple-600 text-white rounded-lg font-semibold hover:shadow-lg hover:shadow-indigo-500/30 transition-all flex items-center justify-center gap-2"
            >
              <Upload size={18} />
              Iniciar Deploy
            </button>
          </div>
        )}
      </div>
    </Modal>
  );
};



const Save = ({ size = 24, className = '' }: any) => (
  <svg xmlns="http://www.w3.org/2000/svg" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
    <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/>
    <polyline points="17 21 17 13 7 13 7 21"/>
    <polyline points="7 3 7 8 15 8"/>
  </svg>
);

const Share2 = ({ size = 24, className = '' }: any) => (
  <svg xmlns="http://www.w3.org/2000/svg" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
    <circle cx="18" cy="5" r="3"/>
    <circle cx="6" cy="12" r="3"/>
    <circle cx="18" cy="19" r="3"/>
    <line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/>
    <line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
  </svg>
);

// Main Component
const Pages: React.FC<PagesProps> = ({ projectId }) => {
  const [sites, setSites] = useState<any[]>([]);
  const [selectedSite, setSelectedSite] = useState<any>(null);
  const [activeTab, setActiveTab] = useState('overview');
  const [showUploadModal, setShowUploadModal] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [expandedBranch, setExpandedBranch] = useState<string | null>(null);
  const [newDomainInput, setNewDomainInput] = useState('');
  const [displayDomain, setDisplayDomain] = useState(''); // Unicode para exibição
  const [availableCerts, setAvailableCerts] = useState<string[]>([]);
  const [showDomainSuggestions, setShowDomainSuggestions] = useState(false);
  const [saving, setSaving] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [selectedFramework, setSelectedFramework] = useState('Static');
  const [envVars, setEnvVars] = useState<Array<{ key: string; value: string }>>([]);
  const [newEnvKey, setNewEnvKey] = useState('');
  const [newEnvValue, setNewEnvValue] = useState('');
  const [activeFolder, setActiveFolder] = useState('');

  // Helper: Converte Punycode (xn--...) para Unicode (legível)
  const punycodeToUnicode = (domain: string): string => {
    if (!domain || !domain.includes('xn--')) return domain;
    try {
      return domain.replace(/xn--[a-z0-9-]+/gi, (match) => {
        try {
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
            return match;
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
    return domain.toLowerCase().trim();
  };

  const fetchAvailableCerts = async () => {
    try {
      const token = localStorage.getItem('cascata_token');
      const certRes = await fetch('/api/control/system/certificates/status', {
        headers: { 'Authorization': `Bearer ${token}` }
      });
      const certData = await certRes.json();
      setAvailableCerts(certData.domains || []);
    } catch (e) {
      console.error("Cert list failed", e);
    }
  };

  const bestCertMatch = selectedSite?.domain ? availableCerts.find(cert => {
    const domain = selectedSite.domain;
    if (cert === domain) return true;
    if (cert.startsWith('*.')) {
      const root = cert.slice(2);
      if (domain.endsWith(root)) {
        const domainParts = domain.split('.');
        const rootParts = root.split('.');
        return domainParts.length === rootParts.length + 1;
      }
    }
    return false;
  }) : null;

  // Inteligência de Autocomplete para Domínios
  const getDomainSuggestions = () => {
    const search = displayDomain.toLowerCase().trim();
    const suggestions: any[] = [];
    const seen = new Set();

    availableCerts.forEach(cert => {
      const unicodeCert = punycodeToUnicode(cert);
      
      if (!search) {
        if (!seen.has(unicodeCert)) {
          suggestions.push({ value: cert, label: unicodeCert, type: cert.startsWith('*.') ? 'wildcard' : 'exact' });
          seen.add(unicodeCert);
        }
        return;
      }

      if (unicodeCert.toLowerCase().includes(search)) {
        if (!seen.has(unicodeCert)) {
          suggestions.push({ value: cert, label: unicodeCert, type: cert.startsWith('*.') ? 'wildcard' : 'exact' });
          seen.add(unicodeCert);
        }
      }

      if (unicodeCert.startsWith('*.')) {
        const root = unicodeCert.slice(2);
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

  const fetchWithAuth = async (url: string, options: any = {}) => {
    const token = localStorage.getItem('cascata_token');
    const headers = {
      'Authorization': `Bearer ${token}`,
      ...options.headers,
    };
    if (options.body && !(options.body instanceof FormData) && typeof options.body === 'object') {
      headers['Content-Type'] = 'application/json';
      options.body = JSON.stringify(options.body);
    }
    const response = await fetch(url, { ...options, headers });
    if (!response.ok) {
      const errData = await response.json().catch(() => ({}));
      throw new Error(errData.error || `HTTP error ${response.status}`);
    }
    return response.json();
  };

  const fetchSites = async () => {
    try {
      setIsLoading(true);
      console.log('Fetching sites from:', `/api/control/projects/${projectId}/sites`);
      const data = await fetchWithAuth(`/api/control/projects/${projectId}/sites`);
      console.log('Sites data received:', data);
      const mapped = (data || []).map((s: any) => ({
        id: s.id,
        name: s.name,
        domain: s.domain || '',
        slug: s.name.toLowerCase().replace(/[^a-z0-9\-]+/g, '-').trim(),
        status: s.status,
        active_folder: s.active_folder || '',
        lastDeploy: s.updated_at ? new Date(s.updated_at).toLocaleString() : 'Sem deploys',
        branch: 'main',
        visitors: Math.floor(Math.random() * 2000) + 100,
        bandwidth: (Math.random() * 2 + 0.1).toFixed(1) + ' GB',
        builds: 1,
        ssl: s.domain && !s.domain.endsWith('.localhost'),
        framework: 'Static',
        frameworkIcon: '📁'
      }));
      console.log('Mapped sites:', mapped);
      setSites(mapped);
      // Select site if none selected, or keep current selection if it still exists
      if (mapped.length > 0) {
        setSelectedSite((current: any) => {
          const stillExists = current ? mapped.find((m: any) => m.id === current.id) : null;
          return stillExists || mapped[0];
        });
      } else {
        setSelectedSite(null);
      }
    } catch (err) {
      console.error("Failed to fetch sites:", err);
      console.error("Error details:", err instanceof Error ? err.message : String(err));
    } finally {
      setIsLoading(false);
    }
  };

  React.useEffect(() => {
    if (projectId) {
      fetchSites();
      fetchAvailableCerts();
    }
  }, [projectId]);

  React.useEffect(() => {
    if (selectedSite) {
      const rawDomain = selectedSite.domain || '';
      setNewDomainInput(rawDomain);
      setDisplayDomain(punycodeToUnicode(rawDomain));
      setActiveFolder(selectedSite.active_folder || '');
    }
  }, [selectedSite]);

  React.useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest('.site-domain-suggestions-container')) {
        setShowDomainSuggestions(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const handleUpdateDomain = async () => {
    if (!selectedSite) return;
    try {
      const data = await fetchWithAuth(`/api/control/projects/${projectId}/sites/${selectedSite.id}`, {
        method: 'PATCH',
        body: { domain: newDomainInput }
      });
      setSites(prev => prev.map(s => s.id === selectedSite.id ? { ...s, domain: data.domain, ssl: data.domain && !data.domain.endsWith('.localhost') } : s));
      setSelectedSite((prev: any) => prev ? { ...prev, domain: data.domain, ssl: data.domain && !data.domain.endsWith('.localhost') } : null);
      alert("Domínio atualizado com sucesso!");
    } catch (err: any) {
      alert("Erro ao atualizar o domínio: " + err.message);
    }
  };

  const handleUpdateActiveFolder = async () => {
    if (!selectedSite) return;
    try {
      const data = await fetchWithAuth(`/api/control/projects/${projectId}/sites/${selectedSite.id}`, {
        method: 'PATCH',
        body: { active_folder: activeFolder }
      });
      setSites(prev => prev.map(s => s.id === selectedSite.id ? { ...s, active_folder: data.active_folder } : s));
      setSelectedSite((prev: any) => prev ? { ...prev, active_folder: data.active_folder } : null);
      alert("Pasta ativa atualizada com sucesso!");
    } catch (err: any) {
      alert("Erro ao atualizar a pasta ativa: " + err.message);
    }
  };

  const handleDeleteSite = async () => {
    if (!selectedSite) return;
    if (!window.confirm(`Tem certeza que deseja excluir o site "${selectedSite.name}"?`)) return;
    try {
      await fetchWithAuth(`/api/control/projects/${projectId}/sites/${selectedSite.id}`, {
        method: 'DELETE'
      });
      const remaining = sites.filter(s => s.id !== selectedSite.id);
      setSites(remaining);
      if (remaining.length > 0) {
        setSelectedSite(remaining[0]);
      } else {
        setSelectedSite(null);
      }
      alert("Site excluído com sucesso!");
    } catch (err: any) {
      alert("Erro ao excluir o site: " + err.message);
    }
  };

  const filteredSites = sites.filter(site => 
    site.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
    site.domain.toLowerCase().includes(searchQuery.toLowerCase())
  );

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-50 via-white to-indigo-50">
      {/* Header */}
      <div className="bg-white border-b border-slate-200 sticky top-0 z-40">
        <div className="max-w-[1800px] mx-auto px-6 py-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-4">
              <div className="w-12 h-12 bg-gradient-to-br from-indigo-500 to-purple-600 rounded-xl flex items-center justify-center shadow-lg shadow-indigo-500/20">
                <Globe size={24} className="text-white" />
              </div>
              <div>
                <h1 className="text-2xl font-bold text-slate-900">Page Hosting</h1>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div className="max-w-[1800px] mx-auto px-6 py-8">
        {/* Stats Cards */}


        {/* Main Content */}
        <div className="grid grid-cols-12 gap-6">
          {/* Sites List */}
          <div className="col-span-12 lg:col-span-5 xl:col-span-4 space-y-6">
            {/* Search */}
            <div className="bg-white rounded-xl p-4 border border-slate-200 shadow-sm">
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" size={18} />
                <input
                  type="text"
                  placeholder="Buscar sites..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="w-full pl-10 pr-4 py-3 border border-slate-300 rounded-xl focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
                />
              </div>
            </div>

            {/* Sites */}
            <div className="bg-white rounded-xl border border-slate-200 shadow-sm overflow-hidden">
              <div className="p-4 border-b border-slate-200">
                <div className="flex items-center justify-between">
                  <h3 className="font-semibold text-slate-900">Sites Hospedados</h3>
                  <div className="flex items-center gap-3">
                    <span className="text-sm text-slate-500">{filteredSites.length} sites</span>
                    <button
                      onClick={() => setShowUploadModal(true)}
                      className="px-4 py-2 bg-indigo-500 text-white rounded-lg font-medium text-sm hover:bg-indigo-600 transition-colors flex items-center gap-2"
                    >
                      <Plus size={16} />
                      NEW
                    </button>
                  </div>
                </div>
              </div>
              
              <div className="divide-y divide-slate-100">
                {filteredSites.map(site => (
                  <div 
                    key={site.id}
                    onClick={() => setSelectedSite(site)}
                    className={`p-4 hover:bg-slate-50 cursor-pointer transition-all ${
                      selectedSite?.id === site.id ? 'bg-indigo-50 border-l-4 border-indigo-500' : ''
                    }`}
                  >
                    <div className="flex items-start gap-4">
                      <div className={`w-12 h-12 bg-gradient-to-br ${colors.primary} rounded-xl flex items-center justify-center shadow-md`}>
                        <span className="text-xl">{site.frameworkIcon}</span>
                      </div>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2 mb-1">
                          <h4 className="font-semibold text-slate-900 truncate">{site.name}</h4>
                          <StatusBadge status={site.status} />
                        </div>
                        <p className="text-sm text-slate-500 truncate">{site.domain}</p>
                      </div>
                      <ChevronRight size={18} className="text-slate-400 flex-shrink-0" />
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {/* Detail Panel */}
          <div className="col-span-12 lg:col-span-7 xl:col-span-8">
            {selectedSite ? (
              <div className="bg-white rounded-xl border border-slate-200 shadow-sm overflow-hidden">
                {/* Site Header */}
                <div className="p-6 border-b border-slate-200 bg-gradient-to-r from-slate-50 to-indigo-50">
                  <div className="flex items-start justify-between">
                    <div className="flex items-center gap-4">
                      <div className={`w-16 h-16 bg-gradient-to-br ${colors.primary} rounded-xl flex items-center justify-center shadow-lg`}>
                        <span className="text-3xl">{selectedSite.frameworkIcon}</span>
                      </div>
                      <div>
                        <div className="flex items-center gap-3 mb-2">
                          <h2 className="text-2xl font-bold text-slate-900">{selectedSite.name}</h2>
                          <StatusBadge status={selectedSite.status} />
                        </div>
                        <p className="text-slate-600 flex items-center gap-2">
                          <Link2 size={14} />
                          {selectedSite.domain}
                        </p>
                        <div className="flex items-center gap-4 mt-3">
                          <span className="flex items-center gap-1.5 text-sm text-slate-500 bg-white px-3 py-1 rounded-full border border-slate-200">
                            <GitBranch size={14} />
                            {selectedSite.branch}
                          </span>
                          <span className="flex items-center gap-1.5 text-sm text-slate-500 bg-white px-3 py-1 rounded-full border border-slate-200">
                            <HardDrive size={14} />
                            {selectedSite.framework}
                          </span>
                          {selectedSite.ssl && (
                            <span className="flex items-center gap-1.5 text-sm text-emerald-600 bg-emerald-50 px-3 py-1 rounded-full border border-emerald-200">
                              <Lock size={14} />
                              SSL
                            </span>
                          )}
                        </div>
                      </div>
                    </div>
                    
                    <div className="flex items-center gap-2">
                      <button className="p-3 bg-white border border-slate-200 rounded-xl hover:bg-slate-50 transition-colors">
                        <ExternalLink size={20} className="text-slate-600" />
                      </button>
                      <button className="p-3 bg-gradient-to-r from-indigo-500 to-purple-600 text-white rounded-xl hover:shadow-lg hover:shadow-indigo-500/30 transition-all flex items-center gap-2">
                        <Play size={18} />
                        Deploy
                      </button>
                    </div>
                  </div>
                </div>

                {/* Tabs */}
                <div className="px-6 border-b border-slate-200 bg-white">
                  <div className="flex items-center gap-1 py-2 overflow-x-auto">
                    <TabButton 
                      active={activeTab === 'overview'} 
                      icon={BarChart3} 
                      label="Visão Geral" 
                      onClick={() => setActiveTab('overview')}
                    />
                    <TabButton 
                      active={activeTab === 'env'} 
                      icon={Key} 
                      label="Variáveis"
                      onClick={() => setActiveTab('env')}
                    />
                    <TabButton 
                      active={activeTab === 'settings'} 
                      icon={Settings} 
                      label="Configurações"
                      onClick={() => setActiveTab('settings')}
                    />
                  </div>
                </div>

                {/* Tab Content */}
                <div className="p-6">
                  {activeTab === 'overview' && (
                    <div className="space-y-6">
                      {/* Quick Stats */}
                      <div className="grid grid-cols-4 gap-4">
                        <div className="bg-gradient-to-br from-purple-50 to-purple-100 rounded-xl p-4 border border-purple-200">
                          <HardDrive size={20} className="text-purple-500 mb-2" />
                          <p className="text-2xl font-bold text-slate-900">{selectedSite.bandwidth}</p>
                          <p className="text-sm text-slate-600">Bandwidth</p>
                        </div>
                        <div className="bg-gradient-to-br from-emerald-50 to-emerald-100 rounded-xl p-4 border border-emerald-200">
                          <Zap size={20} className="text-emerald-500 mb-2" />
                          <p className="text-2xl font-bold text-slate-900">{selectedSite.builds}</p>
                          <p className="text-sm text-slate-600">Builds</p>
                        </div>
                        <div className="bg-gradient-to-br from-amber-50 to-amber-100 rounded-xl p-4 border border-amber-200">
                          <Activity size={20} className="text-amber-500 mb-2" />
                          <p className="text-2xl font-bold text-slate-900">{selectedSite.lastDeploy}</p>
                          <p className="text-sm text-slate-600">Último Deploy</p>
                        </div>
                      </div>

                      {/* Deploy History */}
                      <div className="space-y-4">
                        <h4 className="font-semibold text-slate-900">Histórico de Deploys</h4>
                        <div className="space-y-3">
                          {mockDeployHistory.map(deploy => (
                            <div key={deploy.id} className="flex items-center gap-4 p-4 bg-slate-50 rounded-xl border border-slate-200 hover:bg-slate-100 transition-colors">
                              <div className={`w-10 h-10 rounded-full flex items-center justify-center ${
                                deploy.status === 'success' ? 'bg-emerald-100' : 'bg-red-100'
                              }`}>
                                {deploy.status === 'success' ? (
                                  <CheckCircle size={20} className="text-emerald-500" />
                                ) : (
                                  <XCircle size={20} className="text-red-500" />
                                )}
                              </div>
                              <div className="flex-1">
                                <div className="flex items-center gap-3 mb-1">
                                  <span className="font-semibold text-slate-900">{deploy.version}</span>
                                  <StatusBadge status={deploy.status} />
                                </div>
                                <p className="text-sm text-slate-600">{deploy.message}</p>
                                <div className="flex items-center gap-4 mt-1 text-xs text-slate-400">
                                  <span className="flex items-center gap-1">
                                    <Users size={12} />
                                    {deploy.author}
                                  </span>
                                  <span className="flex items-center gap-1">
                                    <Clock size={12} />
                                    {deploy.time}
                                  </span>
                                  <span className="flex items-center gap-1">
                                    <Terminal size={12} />
                                    {deploy.duration}
                                  </span>
                                </div>
                              </div>
                              <div className="flex items-center gap-2">
                                <button className="p-2 hover:bg-slate-200 rounded-lg transition-colors" title="Ver logs">
                                  <FileCode size={18} className="text-slate-500" />
                                </button>
                                <button className="p-2 hover:bg-slate-200 rounded-lg transition-colors" title="Rollback">
                                  <RefreshCw size={18} className="text-slate-500" />
                                </button>
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>

                      {/* Deploy Preview */}
                      
                    </div>
                  )}

                  {activeTab === 'settings' && (
                    <div className="space-y-6">
                      {/* Main Domain */}
                      <div className="bg-gradient-to-r from-indigo-50 to-purple-50 rounded-xl p-5 border border-indigo-200">
                        <div className="flex items-center justify-between mb-4">
                          <div className="flex items-center gap-3">
                            <Globe2 size={24} className="text-indigo-500" />
                            <div>
                              <h5 className="font-semibold text-slate-900">{selectedSite.domain || 'Nenhum domínio associado'}</h5>
                              <p className="text-sm text-slate-500">Domínio Principal</p>
                            </div>
                          </div>
                          {selectedSite.domain && (
                            <span className={`px-3 py-1 ${bestCertMatch ? 'bg-emerald-100 text-emerald-700 border border-emerald-200' : 'bg-amber-100 text-amber-700 border border-amber-200'} rounded-full text-xs font-medium`}>
                              {bestCertMatch ? '✓ Certificado Válido' : '⚠ Sem Certificado'}
                            </span>
                          )}
                        </div>
                        
                        <div className="grid grid-cols-3 gap-4">
                          <div>
                            <label className="text-xs text-slate-500 uppercase tracking-wide">SLUG</label>
                            <div className="flex items-center gap-2 mt-1">
                              <code className="px-3 py-2 bg-white rounded-lg border border-slate-200 font-mono text-sm text-slate-700 flex-1">
                                {selectedSite.slug}
                              </code>
                              <button onClick={() => {
                                navigator.clipboard.writeText(selectedSite.slug);
                                alert("Slug copiado!");
                              }} className="p-2 hover:bg-white rounded-lg border border-slate-200">
                                <Copy size={16} className="text-slate-400" />
                              </button>
                            </div>
                          </div>
                          <div>
                            <label className="text-xs text-slate-500 uppercase tracking-wide">URL Completa</label>
                            <div className="flex items-center gap-2 mt-1">
                              <code className="px-3 py-2 bg-white rounded-lg border border-slate-200 font-mono text-sm text-slate-700 flex-1 truncate">
                                {selectedSite.domain ? `https://${selectedSite.domain}` : 'Sem URL'}
                              </code>
                              {selectedSite.domain && (
                                <a href={`https://${selectedSite.domain}`} target="_blank" rel="noopener noreferrer" className="p-2 hover:bg-white rounded-lg border border-slate-200">
                                  <ExternalLink size={16} className="text-slate-400" />
                                </a>
                              )}
                            </div>
                          </div>
                          <div>
                            <label className="text-xs text-slate-500 uppercase tracking-wide">Status SSL</label>
                            <div className="mt-2">
                              {bestCertMatch ? (
                                <span className="flex items-center gap-2 text-emerald-600">
                                  <Lock size={16} />
                                  <span className="text-sm font-semibold">Ativo via {bestCertMatch}</span>
                                </span>
                              ) : selectedSite.domain ? (
                                <div className="flex items-center gap-2 text-amber-600">
                                  <AlertTriangle size={16} />
                                  <span className="text-sm font-medium">Sem SSL ativo no Cofre</span>
                                </div>
                              ) : (
                                <span className="text-sm text-slate-400 italic">Configure um domínio</span>
                              )}
                            </div>
                          </div>
                        </div>
                      </div>

                      {/* Configurar Domínio */}
                      <div className="bg-white border border-slate-200 rounded-xl overflow-visible">
                        <div className="p-4 border-b border-slate-200">
                          <h5 className="font-semibold text-slate-900">Configurar Domínio do Site</h5>
                        </div>
                        <div className="p-4 overflow-visible">
                          <div className="flex gap-3 p-4 bg-slate-50 rounded-lg mb-4 items-center overflow-visible relative">
                            <div className="flex-1 relative">
                              <input 
                                type="text" 
                                placeholder="api.my-app.com"
                                value={displayDomain || newDomainInput}
                                onChange={(e) => {
                                  const val = e.target.value;
                                  setDisplayDomain(val);
                                  setNewDomainInput(unicodeToPunycode(val));
                                  setShowDomainSuggestions(true);
                                }}
                                onFocus={() => {
                                  if (availableCerts.length > 0) {
                                    setShowDomainSuggestions(true);
                                  }
                                }}
                                onBlur={() => {
                                  setTimeout(() => setShowDomainSuggestions(false), 200);
                                }}
                                className="w-full px-4 py-2 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent text-sm font-bold text-slate-900"
                              />
                              
                              {showDomainSuggestions && domainSuggestions.length > 0 && (
                                <div className="absolute top-full left-0 right-0 mt-2 bg-white border border-slate-200 rounded-xl shadow-2xl z-[9999] max-h-64 overflow-y-auto">
                                  <div className="p-2">
                                    <div className="flex items-center justify-between px-3 py-1.5 mb-1 border-b border-slate-50">
                                      <p className="text-[9px] font-black text-slate-400 uppercase tracking-widest">Sugestões Inteligentes (Vault)</p>
                                      <span className="text-[9px] font-bold text-indigo-500 bg-indigo-50 px-2 py-0.5 rounded-full">{domainSuggestions.length}</span>
                                    </div>
                                    {domainSuggestions.map((suggestion: any, idx: number) => (
                                      <button
                                        key={`${suggestion.value}-${idx}`}
                                        onMouseDown={(e: React.MouseEvent<HTMLButtonElement>) => {
                                          e.preventDefault();
                                          setNewDomainInput(suggestion.value);
                                          setDisplayDomain(suggestion.label);
                                          setShowDomainSuggestions(false);
                                        }}
                                        className="w-full text-left px-3 py-2 rounded-lg hover:bg-slate-50 transition-all flex items-center justify-between group"
                                      >
                                        <div className="flex items-center gap-2">
                                          <div className={`w-6 h-6 rounded flex items-center justify-center transition-colors ${
                                            suggestion.type === 'expansion' ? 'bg-indigo-500 text-white' : 'bg-slate-100 text-slate-400 group-hover:bg-indigo-100 group-hover:text-indigo-600'
                                          }`}>
                                            {suggestion.type === 'expansion' ? <Zap size={12} /> : <Globe size={12} />}
                                          </div>
                                          <div className="flex flex-col">
                                            <span className="text-xs font-bold text-slate-700 group-hover:text-slate-900">
                                              {suggestion.label}
                                            </span>
                                            {suggestion.type === 'expansion' && (
                                              <span className="text-[8px] text-slate-400">Expandindo wildcard: {suggestion.source}</span>
                                            )}
                                          </div>
                                        </div>
                                        {suggestion.type === 'wildcard' && (
                                          <span className="text-[8px] bg-indigo-100 text-indigo-600 px-2 py-0.5 rounded-full font-bold">Wildcard</span>
                                        )}
                                      </button>
                                    ))}
                                  </div>
                                </div>
                              )}
                            </div>
                            
                            <button 
                              onClick={handleUpdateDomain}
                              disabled={saving}
                              className="px-5 py-2 bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 text-white rounded-lg font-bold text-sm transition-colors flex items-center gap-2"
                            >
                              {saving ? 'Salvando...' : 'Salvar'}
                            </button>
                          </div>
                          
                          <p className="text-xs text-slate-500 flex items-center gap-2">
                            <Info size={14} className="text-indigo-500" />
                            Use sugestões geradas a partir do central de certificados Vault no System Settings para ativar o SSL automático!
                          </p>
                        </div>
                      </div>

                      {/* Build Settings */}
                      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
                        <div className="p-4 border-b border-slate-200 bg-slate-50">
                          <h4 className="font-semibold text-slate-900 flex items-center gap-2">
                            <Cpu size={18} className="text-indigo-500" />
                            Configurações de Build
                          </h4>
                        </div>
                        <div className="p-5 space-y-4">
                          <div>
                            <label className="block text-sm font-medium text-slate-700 mb-2 flex items-center gap-2">
                              <Folder size={16} className="text-indigo-500" />
                              Pasta Ativa (Versionamento)
                            </label>
                            <div className="flex gap-3">
                              <input
                                type="text"
                                value={activeFolder}
                                onChange={(e) => setActiveFolder(e.target.value)}
                                placeholder="Ex: unibloom (nome da pasta descriptografada)"
                                className="flex-1 px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent font-mono text-sm"
                              />
                              <button
                                onClick={handleUpdateActiveFolder}
                                disabled={saving}
                                className="px-5 py-2.5 bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 text-white rounded-lg font-bold text-sm transition-colors"
                              >
                                {saving ? 'Salvando...' : 'Salvar'}
                              </button>
                            </div>
                            <p className="text-xs text-slate-500 mt-2 flex items-center gap-2">
                              <Info size={14} className="text-indigo-500" />
                              Selecione a pasta dentro do diretório do site que contém os arquivos atuais. Útil para versionamento de sites.
                            </p>
                          </div>
                          <div className="grid grid-cols-2 gap-4">
                            <div>
                              <label className="block text-sm font-medium text-slate-700 mb-2">Framework</label>
                              <select 
                                value={selectedFramework}
                                onChange={(e) => setSelectedFramework(e.target.value)}
                                className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
                              >
                                <option value="Next.js">Next.js</option>
                                <option value="React">React</option>
                                <option value="Vue.js">Vue.js</option>
                                <option value="Gatsby">Gatsby</option>
                                <option value="Static">Static (HTML/CSS/JS)</option>
                              </select>
                            </div>
                            {selectedFramework !== 'Static' && (
                              <>
                                <div>
                                  <label className="block text-sm font-medium text-slate-700 mb-2">Node Version</label>
                                  <select className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent">
                                    <option>20.x (LTS)</option>
                                    <option>18.x</option>
                                    <option>16.x</option>
                                  </select>
                                </div>
                                <div>
                                  <label className="block text-sm font-medium text-slate-700 mb-2">Build Command</label>
                                  <input 
                                    type="text" 
                                    defaultValue="npm run build"
                                    className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent font-mono"
                                  />
                                </div>
                                <div>
                                  <label className="block text-sm font-medium text-slate-700 mb-2">Output Directory</label>
                                  <input 
                                    type="text" 
                                    defaultValue="dist"
                                    className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent font-mono"
                                  />
                                </div>
                              </>
                            )}
                          </div>
                          {selectedFramework === 'Static' && (
                            <div className="p-4 bg-indigo-50 border border-indigo-200 rounded-lg">
                              <p className="text-sm text-indigo-700 flex items-center gap-2">
                                <Info size={16} />
                                Sites estáticos não requerem configuração de build. Os arquivos serão servidos diretamente.
                              </p>
                            </div>
                          )}
                        </div>
                      </div>

                      {/* Redirect Rules */}
                      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
                        <div className="p-4 border-b border-slate-200 bg-slate-50">
                          <div className="flex items-center justify-between">
                            <h4 className="font-semibold text-slate-900 flex items-center gap-2">
                              <ArrowUpRight size={18} className="text-indigo-500" />
                              Regras de Redirect
                            </h4>
                            <button className="text-sm text-indigo-600 font-medium">+ Adicionar Regra</button>
                          </div>
                        </div>
                        <div className="p-5">
                          <div className="space-y-3">
                            <div className="flex items-center gap-4 p-3 bg-slate-50 rounded-lg">
                              <code className="px-3 py-1.5 bg-white border border-slate-200 rounded font-mono text-sm">/old-page</code>
                              <ArrowRight size={16} className="text-slate-400" />
                              <code className="px-3 py-1.5 bg-white border border-slate-200 rounded font-mono text-sm">/new-page</code>
                              <span className="px-2 py-1 bg-blue-100 text-blue-700 rounded text-xs font-medium">301</span>
                              <button className="ml-auto p-1.5 hover:bg-white rounded"><Edit3 size={14} className="text-slate-400" /></button>
                            </div>
                          </div>
                        </div>
                      </div>

                      {/* Headers */}
                      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
                        <div className="p-4 border-b border-slate-200 bg-slate-50">
                          <div className="flex items-center justify-between">
                            <h4 className="font-semibold text-slate-900 flex items-center gap-2">
                              <FileCode size={18} className="text-indigo-500" />
                              Headers Personalizados
                            </h4>
                            <button className="text-sm text-indigo-600 font-medium">+ Adicionar Header</button>
                          </div>
                        </div>
                        <div className="p-5">
                          <div className="space-y-2">
                            {['X-Frame-Options: DENY', 'X-Content-Type-Options: nosniff', 'Referrer-Policy: strict-origin-when-cross-origin'].map((header, i) => (
                              <div key={i} className="flex items-center gap-3 p-3 bg-slate-50 rounded-lg">
                                <code className="flex-1 font-mono text-sm text-slate-700">{header}</code>
                                <button className="p-1.5 hover:bg-white rounded"><Edit3 size={14} className="text-slate-400" /></button>
                                <button className="p-1.5 hover:bg-red-50 rounded"><Trash2 size={14} className="text-red-400" /></button>
                              </div>
                            ))}
                          </div>
                        </div>
                      </div>

                      {/* Danger Zone */}
                      <div className="bg-red-50 border border-red-200 rounded-xl overflow-hidden mt-6">
                        <div className="p-4 border-b border-red-200 bg-red-100/50">
                          <h4 className="font-semibold text-red-950 flex items-center gap-2">
                            <Trash2 size={18} className="text-red-600" />
                            Zona de Perigo
                          </h4>
                        </div>
                        <div className="p-5 flex items-center justify-between">
                          <div>
                            <h5 className="font-semibold text-red-900">Excluir Site</h5>
                            <p className="text-sm text-red-700">Esta ação é irreversível e excluirá permanentemente todos os arquivos e configurações deste site.</p>
                          </div>
                          <button 
                            onClick={handleDeleteSite}
                            className="px-4 py-2 bg-red-600 hover:bg-red-750 text-white rounded-lg font-semibold transition-colors"
                          >
                            Excluir Site
                          </button>
                        </div>
                      </div>
                    </div>
                  )}

                  {activeTab === 'env' && (
                    <div className="space-y-6">
                      <div className="flex items-center justify-between">
                        <h4 className="font-semibold text-slate-900">Variáveis de Ambiente</h4>
                        <button className="px-4 py-2 bg-gradient-to-r from-indigo-500 to-purple-600 text-white rounded-lg font-medium text-sm hover:shadow-lg hover:shadow-indigo-500/30 transition-all flex items-center gap-2">
                          <Plus size={16} />
                          Adicionar Variável
                        </button>
                      </div>

                      {/* Add New Variable Form */}
                      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
                        <div className="p-4 bg-slate-50 border-b border-slate-200">
                          <h5 className="font-semibold text-slate-900">Nova Variável</h5>
                        </div>
                        <div className="p-4 space-y-4">
                          <div className="grid grid-cols-2 gap-4">
                            <div>
                              <label className="block text-sm font-medium text-slate-700 mb-2">Chave</label>
                              <input
                                type="text"
                                placeholder="Ex: API_URL"
                                value={newEnvKey}
                                onChange={(e) => setNewEnvKey(e.target.value)}
                                className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent font-mono"
                              />
                            </div>
                            <div>
                              <label className="block text-sm font-medium text-slate-700 mb-2">Valor</label>
                              <input
                                type="text"
                                placeholder="Ex: https://api.example.com"
                                value={newEnvValue}
                                onChange={(e) => setNewEnvValue(e.target.value)}
                                className="w-full px-4 py-2.5 border border-slate-300 rounded-lg focus:ring-2 focus:ring-indigo-500 focus:border-transparent font-mono"
                              />
                            </div>
                          </div>
                          <button
                            onClick={() => {
                              if (newEnvKey && newEnvValue) {
                                setEnvVars([...envVars, { key: newEnvKey, value: newEnvValue }]);
                                setNewEnvKey('');
                                setNewEnvValue('');
                              }
                            }}
                            disabled={!newEnvKey || !newEnvValue}
                            className="px-4 py-2 bg-indigo-500 text-white rounded-lg font-medium text-sm hover:bg-indigo-600 transition-colors disabled:bg-slate-300 disabled:cursor-not-allowed"
                          >
                            Adicionar
                          </button>
                        </div>
                      </div>

                      {/* Variables List */}
                      <div className="bg-white border border-slate-200 rounded-xl overflow-hidden">
                        <div className="p-4 bg-slate-50 border-b border-slate-200 flex items-center justify-between">
                          <h5 className="font-semibold text-slate-900">Variáveis Configuradas ({envVars.length})</h5>
                          {envVars.length > 0 && (
                            <button className="text-sm text-indigo-600 font-medium flex items-center gap-1">
                              <Download size={14} />
                              Exportar .env
                            </button>
                          )}
                        </div>
                        <div className="p-4 space-y-2">
                          {envVars.length === 0 ? (
                            <div className="text-center py-8 text-slate-500">
                              <Database size={48} className="mx-auto mb-3 text-slate-300" />
                              <p>Nenhuma variável configurada</p>
                            </div>
                          ) : (
                            envVars.map((env, i) => (
                              <div key={i} className="flex items-center gap-3 p-3 bg-slate-50 rounded-lg hover:bg-slate-100 transition-colors">
                                <code className="px-3 py-1.5 bg-white rounded border border-slate-200 font-mono text-sm text-slate-700 flex-1">{env.key}</code>
                                <span className="text-slate-400">=</span>
                                <code className="px-3 py-1.5 bg-white rounded border border-slate-200 font-mono text-sm text-slate-500 flex-1 truncate">{env.value}</code>
                                <button className="p-1.5 hover:bg-white rounded"><Copy size={14} className="text-slate-400" /></button>
                                <button 
                                  onClick={() => {
                                    const updated = [...envVars];
                                    updated.splice(i, 1);
                                    setEnvVars(updated);
                                  }}
                                  className="p-1.5 hover:bg-red-50 rounded"
                                >
                                  <Trash2 size={14} className="text-red-400" />
                                </button>
                              </div>
                            ))
                          )}
                        </div>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            ) : (
              <div className="bg-white rounded-xl border border-slate-200 shadow-sm p-12 text-center">
                <div className="w-20 h-20 mx-auto mb-4 bg-gradient-to-br from-slate-100 to-slate-200 rounded-full flex items-center justify-center">
                  <Globe size={40} className="text-slate-400" />
                </div>
                <h3 className="text-xl font-semibold text-slate-900 mb-2">Selecione um Site</h3>
                <p className="text-slate-500 mb-6">Clique em um site para ver seus detalhes e configurações</p>
                <button 
                  onClick={() => setShowUploadModal(true)}
                  className="px-6 py-3 bg-gradient-to-r from-indigo-500 to-purple-600 text-white rounded-lg font-medium hover:shadow-lg hover:shadow-indigo-500/30 transition-all inline-flex items-center gap-2"
                >
                  <Upload size={18} />
                  Upload de Arquivos
                </button>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Modals */}
      <UploadModal isOpen={showUploadModal} onClose={() => setShowUploadModal(false)} projectId={projectId} onUploadSuccess={fetchSites} />
    </div>
  );
};

export default Pages;