# ALTERAÇÕES MANUAIS NO FRONTEND

## Arquivo: `frontend/pages/AutomationManager.tsx`

### 1. LINHA 21 - Adicionar tipo 'action_request'

**DE:**
```typescript
type: 'trigger' | 'query' | 'http' | 'logic' | 'response' | 'transform' | 'data' | 'rpc' | 'convert' | 'email' | 'math';
```

**PARA:**
```typescript
type: 'trigger' | 'query' | 'http' | 'logic' | 'response' | 'transform' | 'data' | 'rpc' | 'convert' | 'email' | 'math' | 'action_request';
```

---

### 2. LINHA 895 - Adicionar label no typeLabels

**DE:**
```typescript
const typeLabels: Record<string, string> = { logic: 'Lógica', http: 'HTTP', rpc: 'RPC', data: 'Dados', query: 'SQL', transform: 'Transform', response: 'Resposta', convert: 'Conversão', email: 'Email', math: 'Math' };
```

**PARA:**
```typescript
const typeLabels: Record<string, string> = { logic: 'Lógica', http: 'HTTP', rpc: 'RPC', data: 'Dados', query: 'SQL', transform: 'Transform', response: 'Resposta', convert: 'Conversão', email: 'Email', math: 'Math', action_request: 'Ação de Banco' };
```

---

### 3. LINHA ~750 - Configuração padrão do novo nó (adicionar após 'math')

Procure por:
```typescript
type === 'math' ? { expression: '', variables: {} } : {},
```

**SUBSTITUA POR:**
```typescript
type === 'math' ? { expression: '', variables: {} } :
   type === 'action_request' ? { operation: 'insert', table: '', body: {}, required: false } : {},
```

---

### 4. LINHA ~905 - Mesma alteração no segundo local (drag & drop)

Procure novamente por:
```typescript
type === 'math' ? { expression: '', variables: {} } : {},
```

E substitua da mesma forma:
```typescript
type === 'math' ? { expression: '', variables: {} } :
   type === 'action_request' ? { operation: 'insert', table: '', body: {}, required: false } : {},
```

---

### 5. LINHA ~1278 - Cores do nó (adicionar ao lado de 'transform')

Procure por:
```typescript
${node.type === 'transform' ? 'hover:border-indigo-500 hover:shadow-indigo-100/50' :
```

**SUBSTITUA POR:**
```typescript
${node.type === 'transform' || node.type === 'action_request' ? 'hover:border-indigo-500 hover:shadow-indigo-100/50' :
```

---

### 6. LINHA ~1295 - Ícone do nó (adicionar Database ao response)

Procure por:
```typescript
node.type === 'response' ? 'bg-emerald-600' :
```

**SUBSTITUA POR:**
```typescript
node.type === 'response' || node.type === 'action_request' ? 'bg-emerald-600' :
```

---

### 7. PAINEL DE CONFIGURAÇÃO (Adicionar após o bloco de 'data')

Procure por `{activeNode.type === 'data' && (` e adicione APÓS o fechamento `)}`:

```typescript
{activeNode.type === 'action_request' && (
   <div className="space-y-8">
      <div className="space-y-4">
         <label className="block text-xs font-semibold text-slate-700 uppercase tracking-wider">Operação</label>
         <select
            value={activeNode.config?.operation || 'insert'}
            onChange={(e) => updateNodeConfig(activeNode.id, { ...activeNode.config, operation: e.target.value })}
            className="cascata-input"
         >
            <option value="insert">INSERT</option>
            <option value="update">UPDATE</option>
            <option value="upsert">UPSERT</option>
            <option value="delete">DELETE</option>
            <option value="select">SELECT</option>
            <option value="exec">EXEC (Raw SQL)</option>
         </select>
      </div>
      <div className="space-y-4">
         <label className="block text-xs font-semibold text-slate-700 uppercase tracking-wider">Tabela</label>
         <input
            type="text"
            value={activeNode.config?.table || ''}
            onChange={(e) => updateNodeConfig(activeNode.id, { ...activeNode.config, table: e.target.value })}
            placeholder="nome_da_tabela"
            className="cascata-input"
         />
      </div>
      <div className="space-y-4">
         <label className="block text-xs font-semibold text-slate-700 uppercase tracking-wider">Body (JSON)</label>
         <textarea
            value={JSON.stringify(activeNode.config?.body || {}, null, 2)}
            onChange={(e) => {
               try {
                  const body = JSON.parse(e.target.value);
                  updateNodeConfig(activeNode.id, { ...activeNode.config, body });
               } catch {}
            }}
            placeholder='{"campo": "valor", "outro": "{{variavel}}"}'
            className="cascata-input font-mono text-sm"
            rows={6}
         />
      </div>
      <div className="flex items-center gap-3">
         <input
            type="checkbox"
            id="required"
            checked={activeNode.config?.required || false}
            onChange={(e) => updateNodeConfig(activeNode.id, { ...activeNode.config, required: e.target.checked })}
            className="w-5 h-5 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
         />
         <label htmlFor="required" className="text-sm text-slate-700">
            Obrigatório (aborta em caso de erro)
         </label>
      </div>
   </div>
)}
```

---

## ✅ BACKEND (Já Implementado)

- ✅ `automation.go` - Constante NodeActionRequest
- ✅ `compiled_nodes_action_request.go` - Executor completo
- ✅ `compiled_automation_service.go` - Registro do builder

---

## 🚀 REBUILD

```bash
cd "/home/cocorico/Documentos/proejetos/cascata go"
sudo docker compose build backend
sudo docker compose up -d backend
```
