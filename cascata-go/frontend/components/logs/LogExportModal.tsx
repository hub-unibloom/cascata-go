import React, { useState, useEffect } from 'react';
import { X, Server, Globe, Database, Cloud, HardDrive, Check, AlertTriangle, Plus, Trash2, RefreshCw, Copy, Eye, EyeOff } from 'lucide-react';

// ==========================================
// MERGE ADITIVO: Backend Powerful + Frontend UXx
// ==========================================

interface LogExportExporterConfig {
  id?: string;
  provider: 'datadog' | 'splunk' | 'loki' | 'elk' | 's3' | 'otlp';
  name: string;
  enabled: boolean;
  endpoint?: string;
  api_key?: string;
  token?: string;
  index?: string;
  source?: string;
  service_name?: string;
  headers?: Record<string, string>;
  batch_size?: number;
  timeout_sec?: number;
  s3_bucket?: string;
  s3_region?: string;
  s3_access_key?: string;
  s3_secret_key?: string;
}

interface LogExportConfig {
  enabled: boolean;
  mode: 'sidecar' | 'native';
  api_key: string;
  exporters: LogExportExporterConfig[];
  fallback_to_file: boolean;
  dead_letter_path?: string;
}

interface LogExportModalProps {
  isOpen: boolean;
  onClose: () => void;
  projectId: string;
  projectSlug: string;
  fetchWithAuth: (url: string, options?: any) => Promise<any>;
  onSuccess: (msg: string) => void;
  onError: (msg: string) => void;
}

const PROVIDERS = [
  { id: 'datadog', name: 'Datadog', icon: Cloud, desc: 'Cloud monitoring and analytics' },
  { id: 'splunk', name: 'Splunk', icon: Database, desc: 'Enterprise log management' },
  { id: 'loki', name: 'Grafana Loki', icon: Server, desc: 'Open source log aggregation' },
  { id: 'elk', name: 'ELK Stack', icon: HardDrive, desc: 'Elasticsearch, Logstash, Kibana' },
  { id: 's3', name: 'AWS S3', icon: HardDrive, desc: 'Object storage for log archival' },
  { id: 'otlp', name: 'Generic OTLP', icon: Globe, desc: 'Any OTLP-compatible endpoint' },
];

const LogExportModal: React.FC<LogExportModalProps> = ({
  isOpen,
  onClose,
  projectId,
  projectSlug,
  fetchWithAuth,
  onSuccess,
  onError,
}) => {
  const [config, setConfig] = useState<LogExportConfig>({
    enabled: false,
    mode: 'sidecar',
    api_key: '',
    exporters: [],
    fallback_to_file: true,
    dead_letter_path: '/var/log/cascata/deadletter',
  });
  const [loading, setLoading] = useState(false);
  const [testingConnection, setTestingConnection] = useState<string | null>(null);
  const [showApiKey, setShowApiKey] = useState(false);
  const [activeTab, setActiveTab] = useState<'general' | 'exporters'>('general');
  const [editingExporter, setEditingExporter] = useState<LogExportExporterConfig | null>(null);

  useEffect(() => {
    if (isOpen) {
      fetchConfig();
    }
  }, [isOpen]);

  const fetchConfig = async () => {
    try {
      const res = await fetchWithAuth(`/api/data/${projectId}/logs/export-config`);
      const data = await res.json();
      if (data.config) {
        setConfig(prev => ({
          ...prev,
          ...data.config,
          exporters: data.config.exporters || [],
          mode: data.config.mode || 'sidecar',
          api_key: data.config.api_key || '',
          fallback_to_file: data.config.fallback_to_file ?? true,
        }));
      }
    } catch (e) {
      console.log('No existing config found');
    }
  };

  const handleSave = async () => {
    setLoading(true);
    try {
      await fetchWithAuth(`/api/data/${projectId}/logs/export-config`, {
        method: 'POST',
        body: JSON.stringify(config),
      });
      onSuccess('Log export configuration saved successfully');
      onClose();
    } catch (e: any) {
      onError('Failed to save configuration: ' + e.message);
    } finally {
      setLoading(false);
    }
  };

  const generateApiKey = async () => {
    try {
      const res = await fetchWithAuth(`/api/data/${projectId}/logs/export-config/api-key`, {
        method: 'POST',
      });
      const data = await res.json();
      setConfig(prev => ({ ...prev, api_key: data.api_key }));
      onSuccess('New API key generated');
    } catch (e: any) {
      onError('Failed to generate API key');
    }
  };

  const testConnection = async (exporter: LogExportExporterConfig) => {
    setTestingConnection(exporter.id || exporter.name);
    try {
      const res = await fetchWithAuth(`/api/data/${projectId}/logs/export-config/test`, {
        method: 'POST',
        body: JSON.stringify({ exporter }),
      });
      const data = await res.json();
      if (data.success) {
        onSuccess(`Connection to ${exporter.name} successful`);
      } else {
        onError(`Connection failed: ${data.error}`);
      }
    } catch (e: any) {
      onError('Connection test failed');
    } finally {
      setTestingConnection(null);
    }
  };

  const addExporter = (provider: string) => {
    const newExporter: LogExportExporterConfig = {
      id: `exp_${Date.now()}`,
      provider: provider as any,
      name: `${provider.toUpperCase()} Export`,
      enabled: true,
      batch_size: 100,
      timeout_sec: 30,
    };
    setEditingExporter(newExporter);
  };

  const saveExporter = () => {
    if (!editingExporter) return;
    const existing = config.exporters.find(e => e.id === editingExporter.id);
    if (existing) {
      setConfig(prev => ({
        ...prev,
        exporters: prev.exporters.map(e => e.id === editingExporter.id ? editingExporter : e),
      }));
    } else {
      setConfig(prev => ({
        ...prev,
        exporters: [...prev.exporters, editingExporter],
      }));
    }
    setEditingExporter(null);
  };

  const removeExporter = (id: string) => {
    setConfig(prev => ({
      ...prev,
      exporters: prev.exporters.filter(e => e.id !== id),
    }));
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    onSuccess('Copied to clipboard');
  };

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-xl z-[400] flex items-center justify-center p-8">
      <div className="bg-white rounded-[2rem] w-full max-w-4xl max-h-[90vh] overflow-hidden flex flex-col shadow-2xl border border-slate-100">
        <header className="p-8 pb-4 border-b border-slate-100 bg-slate-50/50">
          <div className="flex items-center justify-between mb-4">
            <div className="flex items-center gap-4">
              <div className="w-14 h-14 bg-indigo-600 text-white rounded-[1.5rem] flex items-center justify-center shadow-xl">
                <Server size={28} />
              </div>
              <div>
                <h3 className="text-3xl font-black text-slate-900 tracking-tighter">OpenTelemetry Export</h3>
                <p className="text-xs text-indigo-600 font-bold uppercase tracking-widest">Multi-Tenant Log Routing</p>
              </div>
            </div>
            <button onClick={onClose} className="p-3 hover:bg-slate-200 rounded-full transition-all text-slate-400">
              <X size={28} />
            </button>
          </div>

          <div className="flex gap-2 mt-4">
            <button
              onClick={() => setActiveTab('general')}
              className={`px-4 py-2 rounded-xl text-sm font-bold transition-all ${
                activeTab === 'general' ? 'bg-indigo-600 text-white' : 'bg-slate-100 text-slate-600 hover:bg-slate-200'
              }`}
            >
              General Settings
            </button>
            <button
              onClick={() => setActiveTab('exporters')}
              className={`px-4 py-2 rounded-xl text-sm font-bold transition-all ${
                activeTab === 'exporters' ? 'bg-indigo-600 text-white' : 'bg-slate-100 text-slate-600 hover:bg-slate-200'
              }`}
            >
              Exporters ({config.exporters.length})
            </button>
          </div>
        </header>

        <div className="flex-1 overflow-y-auto p-8">
          {activeTab === 'general' ? (
            <div className="space-y-6">
              <div className="flex items-center justify-between p-4 bg-slate-50 rounded-2xl border border-slate-100">
                <div>
                  <h4 className="font-bold text-slate-900">Enable Log Export</h4>
                  <p className="text-sm text-slate-500">Export audit logs to external providers</p>
                </div>
                <button
                  onClick={() => setConfig(prev => ({ ...prev, enabled: !prev.enabled }))}
                  className={`w-14 h-8 rounded-full transition-all ${
                    config.enabled ? 'bg-indigo-600' : 'bg-slate-300'
                  }`}
                >
                  <div className={`w-6 h-6 bg-white rounded-full transition-all transform ${
                    config.enabled ? 'translate-x-7' : 'translate-x-1'
                  }`} />
                </button>
              </div>

              {config.enabled && (
                <>
                  <div className="p-4 bg-slate-50 rounded-2xl border border-slate-100">
                    <label className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-2 block">Export Mode</label>
                    <div className="flex gap-2">
                      <button
                        onClick={() => setConfig(prev => ({ ...prev, mode: 'sidecar' }))}
                        className={`flex-1 p-4 rounded-xl border-2 transition-all text-left ${
                          config.mode === 'sidecar'
                            ? 'border-indigo-600 bg-indigo-50'
                            : 'border-slate-200 hover:border-slate-300'
                        }`}
                      >
                        <Server className="mb-2" size={24} />
                        <div className="font-bold text-slate-900">Sidecar</div>
                        <div className="text-xs text-slate-500">Via OTel Collector</div>
                      </button>
                      <button
                        onClick={() => setConfig(prev => ({ ...prev, mode: 'native' }))}
                        className={`flex-1 p-4 rounded-xl border-2 transition-all text-left ${
                          config.mode === 'native'
                            ? 'border-indigo-600 bg-indigo-50'
                            : 'border-slate-200 hover:border-slate-300'
                        }`}
                      >
                        <Globe className="mb-2" size={24} />
                        <div className="font-bold text-slate-900">Native</div>
                        <div className="text-xs text-slate-500">Direct to provider</div>
                      </button>
                    </div>
                  </div>

                  <div className="p-4 bg-indigo-50 rounded-2xl border border-indigo-100">
                    <div className="flex items-center justify-between mb-2">
                      <label className="text-xs font-bold text-indigo-700 uppercase tracking-wider">Project API Key</label>
                      <button
                        onClick={generateApiKey}
                        className="text-xs font-bold text-indigo-600 hover:text-indigo-800 flex items-center gap-1"
                      >
                        <RefreshCw size={12} /> Regenerate
                      </button>
                    </div>
                    <div className="flex gap-2">
                      <input
                        type={showApiKey ? 'text' : 'password'}
                        value={config.api_key}
                        readOnly
                        className="flex-1 px-4 py-2 bg-white border border-indigo-200 rounded-xl text-sm font-mono"
                      />
                      <button
                        onClick={() => setShowApiKey(!showApiKey)}
                        className="p-2 hover:bg-indigo-100 rounded-xl text-indigo-600"
                      >
                        {showApiKey ? <EyeOff size={18} /> : <Eye size={18} />}
                      </button>
                      <button
                        onClick={() => copyToClipboard(config.api_key)}
                        className="p-2 hover:bg-indigo-100 rounded-xl text-indigo-600"
                      >
                        <Copy size={18} />
                      </button>
                    </div>
                    <p className="text-xs text-indigo-600 mt-2">
                      Header required: <code className="bg-white px-1 rounded">X-API-Key: {config.api_key}</code>
                    </p>
                  </div>

                  <div className="p-4 bg-slate-50 rounded-2xl border border-slate-100">
                    <div className="flex items-center justify-between">
                      <div>
                        <h4 className="font-bold text-slate-900">Fallback to File</h4>
                        <p className="text-sm text-slate-500">Write to dead letter queue on failure</p>
                      </div>
                      <button
                        onClick={() => setConfig(prev => ({ ...prev, fallback_to_file: !prev.fallback_to_file }))}
                        className={`w-14 h-8 rounded-full transition-all ${
                          config.fallback_to_file ? 'bg-indigo-600' : 'bg-slate-300'
                        }`}
                      >
                        <div className={`w-6 h-6 bg-white rounded-full transition-all transform ${
                          config.fallback_to_file ? 'translate-x-7' : 'translate-x-1'
                        }`} />
                      </button>
                    </div>
                  </div>
                </>
              )}
            </div>
          ) : (
            <div className="space-y-4">
              <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
                {PROVIDERS.map(provider => {
                  const Icon = provider.icon;
                  const isActive = config.exporters.some(e => e.provider === provider.id);
                  return (
                    <button
                      key={provider.id}
                      onClick={() => addExporter(provider.id)}
                      className={`p-4 rounded-xl border-2 transition-all text-left ${
                        isActive
                          ? 'border-indigo-600 bg-indigo-50'
                          : 'border-slate-200 hover:border-indigo-300'
                      }`}
                    >
                      <Icon className={`mb-2 ${isActive ? 'text-indigo-600' : 'text-slate-400'}`} size={24} />
                      <div className="font-bold text-slate-900">{provider.name}</div>
                      <div className="text-xs text-slate-500">{provider.desc}</div>
                    </button>
                  );
                })}
              </div>

              {config.exporters.length > 0 && (
                <div className="mt-6 space-y-3">
                  <h4 className="font-bold text-slate-900">Configured Exporters</h4>
                  {config.exporters.map(exporter => (
                    <div key={exporter.id} className="p-4 bg-slate-50 rounded-xl border border-slate-200">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-3">
                          {React.createElement(PROVIDERS.find(p => p.id === exporter.provider)?.icon || Server, {
                            className: exporter.enabled ? 'text-indigo-600' : 'text-slate-400',
                            size: 20
                          })}
                          <div>
                            <div className="font-bold text-slate-900">{exporter.name}</div>
                            <div className="text-xs text-slate-500">
                              {exporter.endpoint || 'Using sidecar collector'}
                            </div>
                          </div>
                        </div>
                        <div className="flex items-center gap-2">
                          <button
                            onClick={() => testConnection(exporter)}
                            disabled={testingConnection === exporter.id}
                            className="p-2 hover:bg-slate-200 rounded-lg text-slate-600 transition-all"
                            title="Test connection"
                          >
                            {testingConnection === exporter.id ? (
                              <RefreshCw size={18} className="animate-spin" />
                            ) : (
                              <Check size={18} />
                            )}
                          </button>
                          <button
                            onClick={() => setEditingExporter(exporter)}
                            className="p-2 hover:bg-indigo-100 rounded-lg text-indigo-600 transition-all"
                            title="Edit"
                          >
                            <Plus size={18} />
                          </button>
                          <button
                            onClick={() => removeExporter(exporter.id!)}
                            className="p-2 hover:bg-rose-100 rounded-lg text-rose-500 transition-all"
                            title="Remove"
                          >
                            <Trash2 size={18} />
                          </button>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>

        <footer className="p-6 border-t border-slate-100 bg-slate-50/50 flex justify-between">
          <button
            onClick={onClose}
            className="px-6 py-3 rounded-xl font-bold text-slate-600 hover:bg-slate-200 transition-all"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={loading}
            className="px-8 py-3 bg-indigo-600 text-white rounded-xl font-bold hover:bg-indigo-700 transition-all disabled:opacity-50 flex items-center gap-2"
          >
            {loading ? <RefreshCw size={18} className="animate-spin" /> : <Check size={18} />}
            Save Configuration
          </button>
        </footer>
      </div>

      {editingExporter && (
        <div className="fixed inset-0 bg-slate-950/60 backdrop-blur-sm z-[500] flex items-center justify-center p-8">
          <div className="bg-white rounded-2xl w-full max-w-lg shadow-2xl border border-slate-100 p-6">
            <h4 className="text-xl font-bold text-slate-900 mb-4">Configure Exporter</h4>

            <div className="space-y-4">
              <div>
                <label className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-1 block">Name</label>
                <input
                  type="text"
                  value={editingExporter.name}
                  onChange={e => setEditingExporter(prev => prev ? { ...prev, name: e.target.value } : null)}
                  className="w-full px-4 py-2 border border-slate-200 rounded-xl focus:ring-2 focus:ring-indigo-500 outline-none"
                />
              </div>

              {config.mode === 'native' && (
                <div>
                  <label className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-1 block">Endpoint URL</label>
                  <input
                    type="text"
                    value={editingExporter.endpoint || ''}
                    onChange={e => setEditingExporter(prev => prev ? { ...prev, endpoint: e.target.value } : null)}
                    placeholder="https://api.datadoghq.com/api/v2/logs"
                    className="w-full px-4 py-2 border border-slate-200 rounded-xl focus:ring-2 focus:ring-indigo-500 outline-none"
                  />
                </div>
              )}

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-1 block">API Key / Token</label>
                  <input
                    type="password"
                    value={editingExporter.api_key || ''}
                    onChange={e => setEditingExporter(prev => prev ? { ...prev, api_key: e.target.value } : null)}
                    className="w-full px-4 py-2 border border-slate-200 rounded-xl focus:ring-2 focus:ring-indigo-500 outline-none"
                  />
                </div>
                <div>
                  <label className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-1 block">Index / Source</label>
                  <input
                    type="text"
                    value={editingExporter.index || ''}
                    onChange={e => setEditingExporter(prev => prev ? { ...prev, index: e.target.value } : null)}
                    placeholder="main"
                    className="w-full px-4 py-2 border border-slate-200 rounded-xl focus:ring-2 focus:ring-indigo-500 outline-none"
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-1 block">Batch Size</label>
                  <input
                    type="number"
                    value={editingExporter.batch_size || 100}
                    onChange={e => setEditingExporter(prev => prev ? { ...prev, batch_size: parseInt(e.target.value) } : null)}
                    className="w-full px-4 py-2 border border-slate-200 rounded-xl focus:ring-2 focus:ring-indigo-500 outline-none"
                  />
                </div>
                <div>
                  <label className="text-xs font-bold text-slate-500 uppercase tracking-wider mb-1 block">Timeout (sec)</label>
                  <input
                    type="number"
                    value={editingExporter.timeout_sec || 30}
                    onChange={e => setEditingExporter(prev => prev ? { ...prev, timeout_sec: parseInt(e.target.value) } : null)}
                    className="w-full px-4 py-2 border border-slate-200 rounded-xl focus:ring-2 focus:ring-indigo-500 outline-none"
                  />
                </div>
              </div>

              <div className="flex items-center justify-between pt-2">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={editingExporter.enabled}
                    onChange={e => setEditingExporter(prev => prev ? { ...prev, enabled: e.target.checked } : null)}
                    className="w-4 h-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                  />
                  <span className="text-sm font-medium text-slate-700">Enabled</span>
                </label>
              </div>
            </div>

            <div className="flex justify-end gap-3 mt-6">
              <button
                onClick={() => setEditingExporter(null)}
                className="px-4 py-2 rounded-xl font-bold text-slate-600 hover:bg-slate-100 transition-all"
              >
                Cancel
              </button>
              <button
                onClick={saveExporter}
                className="px-6 py-2 bg-indigo-600 text-white rounded-xl font-bold hover:bg-indigo-700 transition-all"
              >
                Save Exporter
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default LogExportModal;

