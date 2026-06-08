import React, { useState, useCallback, useMemo } from 'react';
import { 
  Globe, Lock, Unlock, FileText, Code, 
  Settings, Plus, Trash2, Key, Database, Shield,
  ChevronDown, Check, AlertCircle, ExternalLink, Zap,
  Upload, X, Eye, EyeOff, Clock, RefreshCw, Play, Copy,
  CheckCircle2, XCircle, Loader2, Braces, Sparkles, ArrowRight
} from 'lucide-react';
import { VariableMapper } from './VariableMapper';

// Types
interface ResponseFieldMapping {
  sourcePath: string;    // JSON path ex: "data.user.name"
  outputName: string;    // Nome amigável ex: "userName"
  outputType: string;    // Tipo: string, number, boolean, object, array
}

interface HTTPNodeConfig {
  url: string;
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  headers: Record<string, string>;
  queryParams: Record<string, string>;
  bodyType: 'json' | 'form_data' | 'raw' | 'none';
  bodyJSON?: any; // Can be string or object
  bodyRaw?: string;
  authType: 'none' | 'bearer' | 'basic' | 'apikey';
  authConfig?: {
    bearerToken?: string;
    basicUser?: string;
    basicPass?: string;
    apiKeyName?: string;
    apiKeyValue?: string;
  };
  timeout?: number;
  retryCount?: number;
  followRedirects?: boolean;
  validateSSL?: boolean;
  outputMappings?: ResponseFieldMapping[];
}

interface HTTPNodeSimpleProps {
  config: HTTPNodeConfig;
  onChange: (config: HTTPNodeConfig) => void;
  availableNodes?: any[];
  vaultSecrets?: any[];
  projectId?: string;
  testLogs?: any[];
}

const HTTPNodeSimple: React.FC<HTTPNodeSimpleProps> = ({
  config,
  onChange,
  availableNodes = [],
  vaultSecrets = [],
  projectId,
  testLogs = []
}) => {
  const [activeTab, setActiveTab] = useState<'general' | 'body' | 'auth' | 'settings'>('general');
  const [isTesting, setIsTesting] = useState(false);
  const [testResult, setTestResult] = useState<any>(null);
  const [testError, setTestError] = useState<string | null>(null);
  const [showMapper, setShowMapper] = useState((config.outputMappings || []).length > 0);

  // Extrai todos os caminhos de um objeto JSON para o mapper
  const extractPaths = (obj: any, prefix = ''): Array<{ path: string; type: string; example: string }> => {
    const paths: Array<{ path: string; type: string; example: string }> = [];
    if (obj === null || obj === undefined) return paths;

    const type = Array.isArray(obj) ? 'array' : typeof obj;
    if (type !== 'object') {
      return [{ path: prefix || 'root', type, example: String(obj).slice(0, 50) }];
    }

    if (Array.isArray(obj) && obj.length > 0) {
      // Para arrays, mostramos o primeiro item como exemplo
      paths.push({ path: prefix || 'root', type: 'array', example: `[${obj.length} items]` });
      const item = obj[0];
      if (item && typeof item === 'object') {
        Object.keys(item).forEach(key => {
          const subPaths = extractPaths(item[key], prefix ? `${prefix}[].${key}` : `${key}`);
          paths.push(...subPaths);
        });
      }
      return paths;
    }

    Object.keys(obj).forEach(key => {
      const fullPath = prefix ? `${prefix}.${key}` : key;
      const val = obj[key];
      const valType = Array.isArray(val) ? 'array' : (val === null ? 'null' : typeof val);

      if (val !== null && typeof val === 'object') {
        paths.push({ path: fullPath, type: valType, example: Array.isArray(val) ? `[${val.length} items]` : '{...}' });
        paths.push(...extractPaths(val, fullPath));
      } else {
        paths.push({ path: fullPath, type: valType, example: String(val).slice(0, 50) });
      }
    });

    return paths;
  };

  const updateConfig = useCallback((updates: Partial<HTTPNodeConfig>) => {
    onChange({ ...config, ...updates });
  }, [config, onChange]);

  const handleTestRequest = async () => {
    setIsTesting(true);
    setTestResult(null);
    setTestError(null);
    setShowMapper(false);

    const startTime = performance.now();

    try {
      // Preparar headers
      const headers: Record<string, string> = {
        'Accept': 'application/json, */*',
        ...config.headers
      };

      // Auth headers
      if (config.authType === 'bearer' && config.authConfig?.bearerToken) {
        const token = config.authConfig.bearerToken;
        headers['Authorization'] = token.startsWith('Bearer ') ? token : `Bearer ${token}`;
      }
      if (config.authType === 'apikey' && config.authConfig?.apiKeyName && config.authConfig?.apiKeyValue) {
        headers[config.authConfig.apiKeyName] = config.authConfig.apiKeyValue;
      }

      // Basic auth é tratado pelo fetch nativamente
      let credentials: RequestCredentials | undefined;
      let authUser, authPass;
      if (config.authType === 'basic' && config.authConfig?.basicUser && config.authConfig?.basicPass) {
        authUser = config.authConfig.basicUser;
        authPass = config.authConfig.basicPass;
        // Remove vault references for direct test
        if (!authUser.includes('{{') && !authPass.includes('{{')) {
          credentials = 'include';
        }
      }

      // Body
      let body: string | undefined;
      if (config.bodyType === 'json' && config.bodyJSON) {
        const bodyStr = typeof config.bodyJSON === 'string' ? config.bodyJSON : JSON.stringify(config.bodyJSON);
        // Resolve simple variables in test mode (strip {{...}} for test)
        body = bodyStr.replace(/\{\{[^}]+\}\}/g, '"test-value"');
        headers['Content-Type'] = 'application/json';
      } else if (config.bodyType === 'raw' && config.bodyRaw) {
        body = config.bodyRaw.replace(/\{\{[^}]+\}\}/g, 'test-value');
      }

      // URL with resolved variables for test
      let testUrl = config.url.replace(/\{\{[^}]+\}\}/g, 'test-value');

      // Query params
      if (config.queryParams && Object.keys(config.queryParams).length > 0) {
        const urlObj = new URL(testUrl);
        Object.entries(config.queryParams).forEach(([k, v]) => {
          urlObj.searchParams.set(k, (v as string).replace(/\{\{[^}]+\}\}/g, 'test-value'));
        });
        testUrl = urlObj.toString();
      }

      const fetchOptions: RequestInit = {
        method: config.method || 'GET',
        headers,
        body: body || undefined,
        mode: 'cors',
        credentials: 'omit',
        skipCascataInterceptor: true // Bypass global interceptor to avoid CORS issues with external APIs
      } as any;

      const response = await fetch(testUrl, fetchOptions);
      const elapsed = Math.round(performance.now() - startTime);

      let data: any;
      const contentType = response.headers.get('content-type') || '';
      try {
        if (contentType.includes('application/json')) {
          data = await response.json();
        } else {
          data = await response.text();
        }
      } catch (e) {
        data = 'Unable to parse response';
      }

      setTestResult({
        status: response.status,
        statusText: response.statusText,
        time: `${elapsed}ms`,
        headers: Object.fromEntries(response.headers.entries()),
        data,
        rawUrl: testUrl,
      });

      // Auto-show mapper if JSON response
      if (typeof data === 'object' && data !== null) {
        setShowMapper(true);
      }
    } catch (err: any) {
      setTestError(err.message || 'Failed to execute request');
    } finally {
      setIsTesting(false);
    }
  };

  const toggleFieldMapping = (path: string, type: string) => {
    const current: ResponseFieldMapping[] = config.outputMappings || [];
    const exists = current.find((m: ResponseFieldMapping) => m.sourcePath === path);
    if (exists) {
      updateConfig({ outputMappings: current.filter((m: ResponseFieldMapping) => m.sourcePath !== path) });
    } else {
      // Generate friendly name from path
      const friendlyName = path.split('.').pop() || path;
      updateConfig({
        outputMappings: [...current, {
          sourcePath: path,
          outputName: friendlyName,
          outputType: type === 'array' ? 'object' : type
        }]
      });
    }
  };

  const updateMappingName = (path: string, newName: string) => {
    const current: ResponseFieldMapping[] = config.outputMappings || [];
    updateConfig({
      outputMappings: current.map((m: ResponseFieldMapping) =>
        m.sourcePath === path ? { ...m, outputName: newName } : m
      )
    });
  };

  return (
    <div className="space-y-8 animate-in fade-in duration-500">
      {/* Dynamic Header with Status */}
      <div className="flex items-center justify-between bg-slate-900/5 backdrop-blur-md p-2 rounded-[2rem] border border-slate-200/50 shadow-inner">
        <div className="flex gap-1 flex-1">
          {[
            { id: 'general', label: 'Geral', icon: Globe, color: 'text-blue-500' },
            { id: 'auth', label: 'Auth', icon: Lock, color: 'text-amber-500' },
            { id: 'body', label: 'Body', icon: FileText, color: 'text-emerald-500' },
            { id: 'settings', label: 'Avançado', icon: Settings, color: 'text-indigo-500' }
          ].map(tab => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id as any)}
              className={`flex-1 flex items-center justify-center gap-2 py-3.5 px-4 rounded-[1.5rem] text-[10px] font-black uppercase tracking-widest transition-all duration-300 ${
                activeTab === tab.id 
                  ? 'bg-white text-slate-900 shadow-[0_10px_25px_-5px_rgba(0,0,0,0.1)] scale-[1.02]' 
                  : 'text-slate-400 hover:text-slate-600 hover:bg-white/50'
              }`}
            >
              <tab.icon size={14} className={activeTab === tab.id ? tab.color : ''} />
              {tab.label}
            </button>
          ))}
        </div>
      </div>

      {/* Tab Content */}
      <div className="min-h-[400px]">
        {activeTab === 'general' && (
          <div className="space-y-8 animate-in slide-in-from-bottom-4 duration-500">
            {/* URL with VariableMapper */}
            <div className="space-y-4">
              <VariableMapper
                label="URL do Endpoint"
                value={config.url || ''}
                onChange={(val) => updateConfig({ url: val })}
                availableNodes={availableNodes}
                projectId={projectId}
                testLogs={testLogs}
                placeholder="https://api.exemplo.com/v1/..."
                expectedType="string"
              />
              <div className="flex items-center gap-3 px-4 py-2 bg-indigo-50/50 rounded-xl border border-indigo-100/50">
                <Shield size={12} className="text-indigo-400" />
                <span className="text-[10px] font-bold text-indigo-400/80 uppercase tracking-widest">
                  Suporta Variáveis Intercaladas: {"https://{{host}}/api/{{path}}"}
                </span>
              </div>
            </div>

            {/* Method Picker */}
            <div className="space-y-4">
              <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.2em] flex items-center gap-2">
                <Zap size={12} className="text-amber-500" /> Método da Requisição
              </label>
              <div className="grid grid-cols-5 gap-2 p-1.5 bg-slate-50 rounded-2xl border border-slate-100">
                {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(method => (
                  <button
                    key={method}
                    onClick={() => updateConfig({ method: method as any })}
                    className={`py-3 rounded-xl text-[10px] font-black transition-all duration-300 ${
                      config.method === method
                        ? 'bg-slate-900 text-white shadow-xl scale-105'
                        : 'text-slate-400 hover:text-slate-600 hover:bg-white'
                    }`}
                  >
                    {method}
                  </button>
                ))}
              </div>
            </div>

            {/* Headers Section */}
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.2em]">Headers Customizados</label>
                <button
                  onClick={() => {
                    const newHeaders = { ...(config.headers || {}) };
                    const nextIdx = Object.keys(newHeaders).length;
                    newHeaders[`Header-X-${nextIdx}`] = '';
                    updateConfig({ headers: newHeaders });
                  }}
                  className="flex items-center gap-2 px-3 py-1.5 bg-indigo-50 text-indigo-600 rounded-lg text-[9px] font-black uppercase tracking-widest hover:bg-indigo-100 transition-all"
                >
                  <Plus size={12} /> Adicionar
                </button>
              </div>
              
              <div className="space-y-3">
                {Object.entries(config.headers || {}).map(([key, value], idx) => (
                  <div key={idx} className="flex gap-2 items-center group animate-in fade-in slide-in-from-left-2" style={{ animationDelay: `${idx * 50}ms` }}>
                    <div className="flex-1 bg-white border-2 border-slate-100 rounded-xl overflow-hidden focus-within:border-indigo-400 transition-all shadow-sm">
                      <input
                        type="text"
                        placeholder="Key"
                        value={key}
                        onChange={(e) => {
                          const newHeaders = { ...config.headers };
                          delete newHeaders[key];
                          newHeaders[e.target.value] = value;
                          updateConfig({ headers: newHeaders });
                        }}
                        className="w-full px-4 py-3 text-[11px] font-mono font-bold text-slate-600 outline-none bg-transparent"
                      />
                    </div>
                    <ArrowRight size={14} className="text-slate-300" />
                    <div className="flex-[2]">
                       <VariableMapper
                         label=""
                         value={value}
                         onChange={(val) => {
                            const newHeaders = { ...config.headers, [key]: val };
                            updateConfig({ headers: newHeaders });
                         }}
                         availableNodes={availableNodes}
                         projectId={projectId}
                         testLogs={testLogs}
                         placeholder="Value..."
                       />
                    </div>
                    <button
                      onClick={() => {
                        const newHeaders = { ...config.headers };
                        delete newHeaders[key];
                        updateConfig({ headers: newHeaders });
                      }}
                      className="w-10 h-10 flex items-center justify-center text-rose-300 hover:text-rose-500 hover:bg-rose-50 rounded-xl transition-all"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                ))}
                {Object.keys(config.headers || {}).length === 0 && (
                  <div className="py-12 border-2 border-dashed border-slate-100 rounded-[2rem] flex flex-col items-center justify-center gap-3 opacity-60">
                    <div className="w-12 h-12 rounded-full bg-slate-50 flex items-center justify-center">
                       <Plus size={20} className="text-slate-300" />
                    </div>
                    <p className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Nenhum Header configurado</p>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

        {activeTab === 'auth' && (
          <div className="space-y-8 animate-in slide-in-from-bottom-4 duration-500">
            <div className="grid grid-cols-2 gap-4">
              {[
                { id: 'none', label: 'Nenhum', icon: Unlock, desc: 'Sem Auth' },
                { id: 'bearer', label: 'Bearer', icon: Key, desc: 'JWT/OAuth' },
                { id: 'basic', label: 'Basic', icon: Shield, desc: 'User/Pass' },
                { id: 'apikey', label: 'API Key', icon: Database, desc: 'Header/Query' }
              ].map(type => (
                <button
                  key={type.id}
                  onClick={() => updateConfig({ authType: type.id as any })}
                  className={`flex items-center gap-4 p-5 rounded-[2rem] border-2 transition-all duration-300 group ${
                    config.authType === type.id
                      ? 'border-indigo-500 bg-indigo-50/30 shadow-xl shadow-indigo-100/20'
                      : 'border-slate-100 bg-slate-50 hover:border-slate-200'
                  }`}
                >
                  <div className={`w-12 h-12 rounded-2xl flex items-center justify-center transition-transform group-hover:scale-110 ${
                    config.authType === type.id ? 'bg-indigo-600 text-white shadow-lg shadow-indigo-200' : 'bg-white text-slate-400'
                  }`}>
                    <type.icon size={20} />
                  </div>
                  <div className="text-left">
                    <div className={`text-[11px] font-black uppercase tracking-tight ${config.authType === type.id ? 'text-indigo-900' : 'text-slate-600'}`}>{type.label}</div>
                    <div className="text-[9px] font-bold text-slate-400 uppercase tracking-widest">{type.desc}</div>
                  </div>
                </button>
              ))}
            </div>

            {config.authType !== 'none' && (
              <div className="p-8 bg-white border border-slate-100 rounded-[2.5rem] shadow-sm space-y-6 animate-in zoom-in-95">
                {config.authType === 'bearer' && (
                  <VariableMapper
                    label="Bearer Token"
                    value={config.authConfig?.bearerToken || ''}
                    onChange={(val) => updateConfig({ authConfig: { ...config.authConfig, bearerToken: val } })}
                    availableNodes={availableNodes}
                    projectId={projectId}
                    testLogs={testLogs}
                    placeholder="Token ou {{secret}}"
                  />
                )}

                {config.authType === 'basic' && (
                  <div className="grid grid-cols-2 gap-4">
                    <VariableMapper
                      label="Usuário"
                      value={config.authConfig?.basicUser || ''}
                      onChange={(val) => updateConfig({ authConfig: { ...config.authConfig, basicUser: val } })}
                      availableNodes={availableNodes}
                      projectId={projectId}
                      testLogs={testLogs}
                    />
                    <VariableMapper
                      label="Senha"
                      value={config.authConfig?.basicPass || ''}
                      onChange={(val) => updateConfig({ authConfig: { ...config.authConfig, basicPass: val } })}
                      availableNodes={availableNodes}
                      projectId={projectId}
                      testLogs={testLogs}
                    />
                  </div>
                )}

                {config.authType === 'apikey' && (
                  <div className="grid grid-cols-2 gap-4">
                    <VariableMapper
                      label="Nome da Chave"
                      value={config.authConfig?.apiKeyName || ''}
                      onChange={(val) => updateConfig({ authConfig: { ...config.authConfig, apiKeyName: val } })}
                      availableNodes={availableNodes}
                      projectId={projectId}
                      testLogs={testLogs}
                      placeholder="X-API-Key"
                    />
                    <VariableMapper
                      label="Valor"
                      value={config.authConfig?.apiKeyValue || ''}
                      onChange={(val) => updateConfig({ authConfig: { ...config.authConfig, apiKeyValue: val } })}
                      availableNodes={availableNodes}
                      projectId={projectId}
                      testLogs={testLogs}
                    />
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        {activeTab === 'body' && (
          <div className="space-y-8 animate-in slide-in-from-bottom-4 duration-500">
            <div className="flex bg-slate-50 p-1.5 rounded-2xl border border-slate-100">
              {[
                { id: 'none', label: 'Sem Body' },
                { id: 'json', label: 'JSON' },
                { id: 'raw', label: 'Texto' },
                { id: 'form_data', label: 'Form Data' }
              ].map(type => (
                <button
                  key={type.id}
                  onClick={() => updateConfig({ bodyType: type.id as any })}
                  className={`flex-1 py-2.5 rounded-xl text-[9px] font-black uppercase tracking-widest transition-all ${
                    (config.bodyType || 'none') === type.id
                      ? 'bg-white text-slate-900 shadow-md'
                      : 'text-slate-400 hover:text-slate-600'
                  }`}
                >
                  {type.label}
                </button>
              ))}
            </div>

            {config.bodyType === 'json' && (
              <div className="space-y-4">
                <div className="flex items-center justify-between">
                  <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.2em] flex items-center gap-2">
                    <Braces size={12} className="text-amber-500" /> JSON Payload
                  </label>
                  <div className="flex gap-2">
                    <button 
                       onClick={() => {
                          try {
                             const obj = typeof config.bodyJSON === 'string' ? JSON.parse(config.bodyJSON) : config.bodyJSON;
                             updateConfig({ bodyJSON: JSON.stringify(obj, null, 2) });
                          } catch(e) {}
                       }}
                       className="text-[9px] font-black text-indigo-400 uppercase hover:text-indigo-600"
                    >
                      Beautify
                    </button>
                  </div>
                </div>
                <div className="relative group">
                  <div className="absolute -inset-1 bg-gradient-to-r from-emerald-500 to-teal-500 rounded-3xl blur opacity-0 group-focus-within:opacity-10 transition duration-500"></div>
                  <textarea
                    value={typeof config.bodyJSON === 'string' ? config.bodyJSON : JSON.stringify(config.bodyJSON, null, 2)}
                    onChange={(e) => updateConfig({ bodyJSON: e.target.value })}
                    placeholder='{ "id": "{{$nodes.trigger.payload.id}}", "status": "active" }'
                    className="relative w-full h-[300px] bg-slate-900 border-2 border-slate-800 rounded-[2rem] p-8 text-xs font-mono text-emerald-400 outline-none focus:border-emerald-500 transition-all custom-scrollbar shadow-2xl"
                  />
                </div>
                <p className="text-[9px] font-medium text-slate-400 italic">
                  * Você pode usar variáveis {"{{...}}"} dentro de strings JSON.
                </p>
              </div>
            )}

            {config.bodyType === 'raw' && (
              <div className="space-y-4">
                <label className="text-[10px] font-black text-slate-400 uppercase tracking-[0.2em]">Conteúdo Bruto</label>
                <textarea
                  value={config.bodyRaw || ''}
                  onChange={(e) => updateConfig({ bodyRaw: e.target.value })}
                  placeholder="Texto plano ou XML..."
                  className="w-full h-[300px] bg-slate-900 border-2 border-slate-800 rounded-[2rem] p-8 text-xs font-mono text-white outline-none focus:border-indigo-500 transition-all"
                />
              </div>
            )}

            {config.bodyType === 'none' && (
              <div className="flex flex-col items-center justify-center py-20 opacity-30 gap-6">
                <div className="w-24 h-24 rounded-full border-4 border-dashed border-slate-300 flex items-center justify-center">
                   <XCircle size={40} className="text-slate-300" />
                </div>
                <div className="text-center space-y-2">
                   <h3 className="text-sm font-black text-slate-400 uppercase tracking-widest">Sem Body</h3>
                   <p className="text-[10px] font-bold text-slate-300 max-w-[200px]">Requisições GET geralmente não possuem corpo.</p>
                </div>
              </div>
            )}
          </div>
        )}

        {activeTab === 'settings' && (
          <div className="space-y-8 animate-in slide-in-from-bottom-4 duration-500">
             <div className="grid grid-cols-1 gap-6">
                <div className="p-6 bg-slate-50 rounded-3xl border border-slate-100 flex items-center justify-between">
                   <div>
                      <div className="text-[11px] font-black text-slate-900 uppercase">Validar SSL</div>
                      <div className="text-[9px] text-slate-400 font-bold uppercase tracking-widest">Recomendado para produção</div>
                   </div>
                   <button 
                      onClick={() => updateConfig({ validateSSL: !config.validateSSL })}
                      className={`w-14 h-8 rounded-full transition-all flex items-center px-1 ${config.validateSSL !== false ? 'bg-indigo-600 justify-end' : 'bg-slate-200 justify-start'}`}
                   >
                      <div className="w-6 h-6 bg-white rounded-full shadow-md" />
                   </button>
                </div>

                <div className="p-6 bg-slate-50 rounded-3xl border border-slate-100 flex items-center justify-between">
                   <div>
                      <div className="text-[11px] font-black text-slate-900 uppercase">Seguir Redirecionamentos</div>
                      <div className="text-[9px] text-slate-400 font-bold uppercase tracking-widest">Auto Follow 301/302</div>
                   </div>
                   <button 
                      onClick={() => updateConfig({ followRedirects: !config.followRedirects })}
                      className={`w-14 h-8 rounded-full transition-all flex items-center px-1 ${config.followRedirects !== false ? 'bg-indigo-600 justify-end' : 'bg-slate-200 justify-start'}`}
                   >
                      <div className="w-6 h-6 bg-white rounded-full shadow-md" />
                   </button>
                </div>

                <div className="grid grid-cols-2 gap-4">
                   <div className="space-y-3">
                      <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Timeout (ms)</label>
                      <input 
                         type="number"
                         value={config.timeout || 30000}
                         onChange={(e) => updateConfig({ timeout: parseInt(e.target.value) })}
                         className="w-full bg-white border-2 border-slate-100 rounded-xl px-4 py-3 text-[11px] font-bold outline-none focus:border-indigo-400"
                      />
                   </div>
                   <div className="space-y-3">
                      <label className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Retentativas</label>
                      <input 
                         type="number"
                         value={config.retryCount || 0}
                         onChange={(e) => updateConfig({ retryCount: parseInt(e.target.value) })}
                         className="w-full bg-white border-2 border-slate-100 rounded-xl px-4 py-3 text-[11px] font-bold outline-none focus:border-indigo-400"
                      />
                   </div>
                </div>
             </div>
          </div>
        )}
      </div>

      {/* Action Footer: Test Request */}
      <div className="pt-8 border-t border-slate-100">
        <button
          onClick={handleTestRequest}
          disabled={isTesting || !config.url}
          className={`w-full py-5 rounded-[2rem] font-black text-[11px] uppercase tracking-[0.3em] transition-all flex items-center justify-center gap-4 shadow-xl active:scale-95 ${
            isTesting 
              ? 'bg-slate-100 text-slate-400 cursor-not-allowed' 
              : 'bg-indigo-600 text-white hover:bg-indigo-700 shadow-indigo-200'
          }`}
        >
          {isTesting ? <Loader2 size={18} className="animate-spin" /> : <Play size={18} />}
          {isTesting ? 'Executando Chamada...' : 'Testar Requisição'}
        </button>

        {testError && (
          <div className="mt-6 p-6 bg-rose-50 rounded-[2rem] border border-rose-200 animate-in zoom-in-95">
            <div className="flex items-center gap-3 mb-2">
              <AlertCircle size={16} className="text-rose-500" />
              <span className="text-[11px] font-black text-rose-600 uppercase tracking-widest">Erro na Requisição</span>
            </div>
            <p className="text-[10px] text-rose-500 font-mono">{testError}</p>
            <p className="text-[9px] text-rose-400 mt-2">
              Nota: Requisições cross-origin (CORS) podem ser bloqueadas pelo navegador. APIs com CORS habilitado funcionarão corretamente.
            </p>
          </div>
        )}

        {testResult && (
          <div className="mt-6 space-y-6">
            {/* Response Card */}
            <div className="p-6 bg-slate-900 rounded-[2rem] border border-slate-800 animate-in zoom-in-95 overflow-hidden relative">
              <div className="absolute top-0 right-0 p-4 opacity-10">
                 <Sparkles size={40} className="text-emerald-400" />
              </div>
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-3">
                  <span className={`px-2 py-0.5 rounded text-[9px] font-black uppercase ${testResult.status < 300 ? 'bg-emerald-500/20 text-emerald-400' : 'bg-rose-500/20 text-rose-400'}`}>
                    HTTP {testResult.status} {testResult.statusText}
                  </span>
                  <span className="text-[9px] font-black text-slate-500 uppercase tracking-widest">{testResult.time}</span>
                </div>
                <button onClick={() => { setTestResult(null); setShowMapper(false); }} className="text-slate-500 hover:text-white">
                   <X size={14} />
                </button>
              </div>
              <pre className="text-[10px] font-mono text-emerald-400/90 overflow-x-auto whitespace-pre-wrap leading-relaxed max-h-[300px] custom-scrollbar">
                {typeof testResult.data === 'object'
                  ? JSON.stringify(testResult.data, null, 2)
                  : testResult.data}
              </pre>
            </div>

            </div>
        )}

        {/* Response Field Mapper - MOVED OUTSIDE testResult block */}
        {showMapper && (
          <div className="mt-6 p-6 bg-white rounded-[2rem] border border-indigo-100 shadow-lg shadow-indigo-50 animate-in slide-in-from-bottom-4">
            <div className="flex items-center justify-between mb-6">
              <div>
                <h4 className="text-[11px] font-black text-slate-900 uppercase tracking-widest flex items-center gap-2">
                  <Database size={14} className="text-indigo-500" />
                  Mapeador de Campos de Resposta
                </h4>
                <p className="text-[9px] text-slate-500 mt-1">Selecione os campos que serão expostos como outputs para outros nós usarem.</p>
              </div>
              <button
                onClick={() => setShowMapper(false)}
                className="text-[9px] font-black text-slate-400 hover:text-slate-600 uppercase"
              >
                Ocultar Árvore
              </button>
            </div>

            {testResult?.data && typeof testResult.data === 'object' ? (
              <div className="space-y-2 max-h-[400px] overflow-y-auto custom-scrollbar">
                {extractPaths(testResult.data).map((field, idx) => {
                  const isMapped = (config.outputMappings || []).find(m => m.sourcePath === field.path);
                  return (
                    <div
                      key={field.path}
                      className={`flex items-center gap-3 p-3 rounded-xl border transition-all cursor-pointer ${
                        isMapped
                          ? 'border-indigo-300 bg-indigo-50'
                          : 'border-slate-100 hover:border-indigo-200 hover:bg-slate-50'
                      }`}
                      onClick={() => toggleFieldMapping(field.path, field.type)}
                    >
                      <div className={`w-5 h-5 rounded-md border-2 flex items-center justify-center transition-colors ${
                        isMapped ? 'bg-indigo-500 border-indigo-500' : 'border-slate-300'
                      }`}>
                        {isMapped && <Check size={12} className="text-white" />}
                      </div>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <code className="text-[10px] font-mono text-slate-700 font-bold truncate">{field.path}</code>
                          <span className="text-[8px] font-black uppercase px-1.5 py-0.5 rounded bg-slate-100 text-slate-500 tracking-wider">{field.type}</span>
                        </div>
                        <p className="text-[9px] text-slate-400 truncate mt-0.5">{field.example}</p>
                      </div>
                      {isMapped && (
                        <input
                          type="text"
                          value={isMapped.outputName}
                          onClick={(e) => e.stopPropagation()}
                          onChange={(e) => updateMappingName(field.path, e.target.value)}
                          className="w-24 px-2 py-1 text-[10px] font-mono bg-white border border-indigo-200 rounded-lg outline-none focus:border-indigo-500"
                          placeholder="nome"
                        />
                      )}
                    </div>
                  );
                })}
              </div>
            ) : (
              <div className="py-6 text-center bg-slate-50 rounded-2xl border border-dashed border-slate-200">
                <p className="text-[10px] font-black text-slate-400 uppercase tracking-widest italic opacity-60">
                  Execute um teste para mapear novos campos da resposta JSON.
                </p>
              </div>
            )}

            {(config.outputMappings || []).length > 0 && (
              <div className="mt-4 p-4 bg-indigo-50/50 rounded-xl border border-indigo-100">
                <p className="text-[9px] font-black text-indigo-600 uppercase tracking-widest mb-2">Campos Mapeados ({config.outputMappings?.length})</p>
                <div className="flex flex-wrap gap-2">
                  {config.outputMappings?.map(m => (
                    <span key={m.sourcePath} className="group flex items-center gap-2 px-2 py-1 bg-white border border-indigo-200 rounded-lg text-[9px] font-mono text-indigo-700 hover:border-indigo-400 transition-colors">
                      <span className="font-bold">{m.outputName}</span>
                      <span className="text-slate-400">→ {m.sourcePath}</span>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          updateConfig({ outputMappings: (config.outputMappings || []).filter(item => item.sourcePath !== m.sourcePath) });
                        }}
                        className="text-slate-300 hover:text-rose-500 transition-colors"
                      >
                        <X size={10} />
                      </button>
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {/* Show existing mappings even when mapper is closed - MOVED OUTSIDE testResult block */}
        {!showMapper && (config.outputMappings || []).length > 0 && (
          <div className="mt-6 p-6 bg-white rounded-[2rem] border border-indigo-100 shadow-lg shadow-indigo-50 animate-in slide-in-from-bottom-4">
            <div className="flex items-center justify-between mb-6">
              <div>
                <h4 className="text-[11px] font-black text-slate-900 uppercase tracking-widest flex items-center gap-2">
                  <Database size={14} className="text-indigo-500" />
                  Mapeador de Campos de Resposta
                </h4>
                <p className="text-[9px] text-slate-500 mt-1">Campos já configurados como outputs.</p>
              </div>
              <button
                onClick={() => setShowMapper(true)}
                className="text-[9px] font-black text-indigo-600 hover:text-indigo-800 uppercase"
              >
                Mostrar Árvore
              </button>
            </div>

            <div className="p-4 bg-indigo-50/50 rounded-xl border border-indigo-100">
              <p className="text-[9px] font-black text-indigo-600 uppercase tracking-widest mb-2">Campos Mapeados ({config.outputMappings?.length})</p>
              <div className="flex flex-wrap gap-2">
                {config.outputMappings?.map(m => (
                  <span key={m.sourcePath} className="group flex items-center gap-2 px-2 py-1 bg-white border border-indigo-200 rounded-lg text-[9px] font-mono text-indigo-700 hover:border-indigo-400 transition-colors">
                    <span className="font-bold">{m.outputName}</span>
                    <span className="text-slate-400">→ {m.sourcePath}</span>
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        updateConfig({ outputMappings: (config.outputMappings || []).filter(item => item.sourcePath !== m.sourcePath) });
                      }}
                      className="text-slate-300 hover:text-rose-500 transition-colors"
                    >
                      <X size={10} />
                    </button>
                  </span>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

export { HTTPNodeSimple };
