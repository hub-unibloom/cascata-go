import React, { useState, useEffect } from 'react';
import {
   Activity, Workflow, Plus, Trash2, Settings, History, Zap, Save, ArrowRight, Radio
} from 'lucide-react';
import AutomationOverviewView from './automation-manager/AutomationOverviewView';
import NexusArchitect from '../components/NexusArchitect';
import {
   type Automation,
   type AutomationStats,
   type ExecutionRun,
   type NexusNode,
   type NexusSavePayload,
   type StepLog
} from './automation-manager/types';

const AutomationManager: React.FC<{ projectId: string }> = ({ projectId }) => {
   const [automations, setAutomations] = useState<Automation[]>([]);
   const [loading, setLoading] = useState(true);
   const [view, setView] = useState<'list' | 'composer'>('list');
   const [activeTab, setActiveTab] = useState<'workflows' | 'runs'>('workflows');
   const [runs, setRuns] = useState<ExecutionRun[]>([]);
   const [stats, setStats] = useState<Record<string, AutomationStats>>({});
   const [editingAutomation, setEditingAutomation] = useState<Partial<Automation> | null>(null);
   const [submitting, setSubmitting] = useState(false);
   const [error, setError] = useState<string | null>(null);
   const [success, setSuccess] = useState<string | null>(null);
   const [selectedExecutionId, setSelectedExecutionId] = useState<string | null>(null);
   const [stepLogs, setStepLogs] = useState<StepLog[]>([]);
   const [loadingStepLogs, setLoadingStepLogs] = useState(false);
   const [conflictModal, setConflictModal] = useState({ show: false, automationName: '', conflictingId: '', conflictingName: '' });

   // API Actions
   const fetchAutomations = async () => {
      try {
         const res = await fetch(`/api/data/${projectId}/automations`, {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         const data = await res.json();
         setAutomations(Array.isArray(data) ? data : []);
      } catch (e) { console.error("Automations fetch error"); }
   };

   const fetchRuns = async (automationId?: string | null) => {
      try {
         const url = automationId
            ? `/api/data/${projectId}/automations/runs?automation_id=${automationId}`
            : `/api/data/${projectId}/automations/runs`;
         const res = await fetch(url, {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         const data = await res.json();
         setRuns(Array.isArray(data) ? data : []);
      } catch (e) { console.error("Runs fetch error"); }
   };

   const fetchStats = async () => {
      try {
         const res = await fetch(`/api/data/${projectId}/automations/stats`, {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         const data = await res.json();
         if (data && typeof data === 'object') setStats(data);
      } catch (e) { console.error("Stats fetch error"); }
   };

   /**
    * handleSaveFromArchitect — Recebe o payload DIRETAMENTE do NexusArchitect.
    * 
    * O NexusArchitect é o dono absoluto dos nós. Ele envia o grafo completo
    * com posições, conexões, configurações e nome. Nenhum estado intermediário
    * é necessário — eliminando o bug de "estado assíncrono desatualizado".
    */
   const handleSaveFromArchitect = async (payload: NexusSavePayload) => {
      if (!editingAutomation) return;
      setSubmitting(true);
      try {
         const isUpdate = !!editingAutomation.id;
         const url = isUpdate
            ? `/api/data/${projectId}/automations/${editingAutomation.id}`
            : `/api/data/${projectId}/automations`;

         const body = {
            ...editingAutomation,
            name: payload.name,
            nodes: payload.nodes,
            edges: payload.edges || [],
            execution_mode: payload.execution_mode
         };

         const res = await fetch(url, {
            method: isUpdate ? 'PUT' : 'POST',
            headers: {
               'Content-Type': 'application/json',
               'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
            },
            body: JSON.stringify(body)
         });

         if (!res.ok) {
            const errData = await res.json().catch(() => ({}));
            throw new Error(errData.error || "Falha ao salvar blueprint");
         }

         const savedAutomation = await res.json();

         // Atualizar o editingAutomation com os dados salvos (inclui ID para novas automações)
         setEditingAutomation(savedAutomation);

         setSuccess("Blueprint salvo com sucesso!");
         setTimeout(() => setSuccess(null), 3000);
         fetchAutomations();
      } catch (e: any) {
         setError(e.message);
         setTimeout(() => setError(null), 5000);
      } finally {
         setSubmitting(false);
      }
   };

   const handleDelete = async (id: string) => {
      if (!window.confirm('Excluir esta orquestração permanentemente?')) return;
      try {
         const res = await fetch(`/api/data/${projectId}/automations/${id}`, {
            method: 'DELETE',
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         if (!res.ok) throw new Error('Falha ao excluir');
         setAutomations(prev => prev.filter(a => a.id !== id));
         setSuccess('Workflow removido.');
         setTimeout(() => setSuccess(null), 3000);
      } catch (e: any) { setError(e.message); setTimeout(() => setError(null), 5000); }
   };

   const handleActivate = async (auto: Automation) => {
      setSubmitting(true);
      try {
         const res = await fetch(`/api/data/${projectId}/automations/${auto.id}/activate`, {
            method: 'POST',
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         if (!res.ok) throw new Error("Erro ao ativar");
         setSuccess("Orquestração ativada!");
         setTimeout(() => setSuccess(null), 3000);
         fetchAutomations();
      } catch (e: any) { setError(e.message); setTimeout(() => setError(null), 5000); }
      finally { setSubmitting(false); }
   };

   const handleDeactivate = async (auto: Automation) => {
      setSubmitting(true);
      try {
         const res = await fetch(`/api/data/${projectId}/automations/${auto.id}/deactivate`, {
            method: 'POST',
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         if (!res.ok) throw new Error("Erro ao desativar");
         setSuccess("Orquestração desativada.");
         setTimeout(() => setSuccess(null), 3000);
         fetchAutomations();
      } catch (e: any) { setError(e.message); setTimeout(() => setError(null), 5000); }
      finally { setSubmitting(false); }
   };

   const handleToggle = async (auto: Automation) => {
      try {
         await fetch(`/api/data/${projectId}/automations/${auto.id}/toggle`, {
            method: 'POST',
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         fetchAutomations();
      } catch (e) { console.error("Toggle error"); }
   };

   const fetchStepLogs = async (executionId: string) => {
      setLoadingStepLogs(true);
      try {
         const res = await fetch(`/api/data/${projectId}/automations/runs/${executionId}/logs`, {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         const data = await res.json();
         setStepLogs(Array.isArray(data) ? data : []);
         return data;
      } catch (e) { console.error("Step logs fetch error"); }
      finally { setLoadingStepLogs(false); }
   };

   const handleTestRun = async (automationId: string, payload?: any) => {
      try {
         const res = await fetch(`/api/data/${projectId}/automations/${automationId}/test`, {
            method: 'POST',
            headers: {
               'Content-Type': 'application/json',
               'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
            },
            body: JSON.stringify(payload || {})
         });
         const data = await res.json();
         if (!res.ok) throw new Error(data.error || 'Test failed');
         return data;
      } catch (e: any) {
         console.error("Test run error:", e);
         throw e;
      }
   };

   const lastRunIds = React.useRef<Record<string, string>>({});

   const handleStartListening = async (automationId: string) => {
      try {
         const res = await fetch(`/api/data/${projectId}/automations/runs?automation_id=${automationId}`, {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         const data = await res.json();
         if (Array.isArray(data) && data.length > 0) {
            lastRunIds.current[automationId] = data[0].id;
         } else {
            lastRunIds.current[automationId] = 'none';
         }
         return true;
      } catch (e) {
         console.error("Error starting listen mode:", e);
         return false;
      }
   };

   const handleStopListening = async (automationId: string) => {
      delete lastRunIds.current[automationId];
      return true;
   };

   const handleCheckExecution = async (automationId: string) => {
      try {
         const res = await fetch(`/api/data/${projectId}/automations/runs?automation_id=${automationId}`, {
            headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
         });
         const data = await res.json();
         if (Array.isArray(data) && data.length > 0) {
            const latestId = data[0].id;
            if (latestId !== lastRunIds.current[automationId]) {
               return latestId;
            }
         }
      } catch (e) {
         console.error("Error checking execution:", e);
      }
      return null;
   };

   const handleCreateNew = () => {
      const baseName = 'Nova Orquestração Nexus';
      let uniqueName = baseName;
      let counter = 1;
      const existingNames = automations.map(a => a.name);
      while (existingNames.includes(uniqueName)) {
         uniqueName = `${baseName} ${counter}`;
         counter++;
      }

      setEditingAutomation({
         name: uniqueName,
         description: 'Fluxo de alta performance',
         trigger_type: 'API_INTERCEPT',
         trigger_config: { table: '*', event: '*' },
         is_active: false,
         execution_mode: 'linear',
         nodes: []  // Canvas vazio — o NexusArchitect abrirá o modal de seleção
      });
      setView('composer');
   };

   const handleOpenExisting = (auto: Automation) => {
      setEditingAutomation(auto);
      setView('composer');
   };

   useEffect(() => {
      setLoading(true);
      Promise.all([fetchAutomations(), fetchRuns(), fetchStats()]).then(() => setLoading(false));
   }, [projectId]);

   // Composer View — NexusArchitect é o dono absoluto do canvas
   if (view === 'composer') {
      return (
         <div className="w-full h-screen min-h-[800px] animate-in fade-in duration-500">
            <NexusArchitect
               projectId={projectId}
               automation={editingAutomation}
               nodes={editingAutomation?.nodes || []}
               onSave={handleSaveFromArchitect}
               onBack={() => setView('list')}
               onTestRun={handleTestRun}
               onFetchStepLogs={fetchStepLogs}
               onStartListening={handleStartListening}
               onStopListening={handleStopListening}
               onCheckExecution={handleCheckExecution}
            />
            {(success || error) && (
               <div className={`fixed top-8 left-1/2 -translate-x-1/2 z-[1000] px-8 py-4 rounded-full shadow-2xl flex items-center gap-3 animate-in slide-in-from-top-4 ${error ? 'bg-rose-600' : 'bg-slate-900'} text-white`}>
                  <Zap size={18} className={error ? '' : 'text-indigo-400'} />
                  <span className="text-xs font-black uppercase tracking-widest">{success || error}</span>
               </div>
            )}
         </div>
      );
   }

   return (
      <div className="p-6 lg:p-10 w-full min-h-screen space-y-12 pb-40 animate-in fade-in duration-500">
         {/* Page Header moved here to allow complete transition */}
         <header className="flex items-end justify-between gap-8">
            <div>
               <h2 className="text-4xl font-black text-slate-900 tracking-tighter">Events</h2>
               <div className="flex items-center gap-6 mt-4">
                  <div className="flex items-center gap-2 pb-2 border-b-2 border-indigo-600 text-indigo-600">
                     <Radio size={16} />
                     <span className="text-[10px] font-black uppercase tracking-widest">Contatos Internos</span>
                  </div>
               </div>
            </div>
         </header>

         <AutomationOverviewView
            success={success}
            error={error}
            activeTab={activeTab}
            setActiveTab={setActiveTab}
            handleCreateNew={handleCreateNew}
            loading={loading}
            automations={automations}
            handleOpenExisting={handleOpenExisting}
            handleDelete={handleDelete}
            handleActivate={handleActivate}
            handleDeactivate={handleDeactivate}
            handleToggle={handleToggle}
            stats={stats}
            fetchRuns={fetchRuns}
            fetchStepLogs={fetchStepLogs}
            runs={runs}
            setSelectedExecutionId={setSelectedExecutionId}
            selectedExecutionId={selectedExecutionId}
            setStepLogs={setStepLogs}
            stepLogs={stepLogs}
            loadingStepLogs={loadingStepLogs}
            conflictModal={conflictModal}
            setConflictModal={setConflictModal}
            submitting={submitting}
         />
      </div>
   );
};

export default AutomationManager;