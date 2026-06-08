import React, { useState, useMemo, useCallback, useEffect } from 'react';
import { Search, List, X, Loader2, CheckCircle2, Plus, Trash2, Edit2, AlertTriangle, Tag, ChevronDown, ChevronUp } from 'lucide-react';

// PostgreSQL Identifier Normalization (for ENUM names and values)
const normalizePostgresIdentifier = (input: string): string => {
  if (!input) return '';
  
  return input
    // Remove accents/diacritics (normalize to NFD and remove combining marks)
    .normalize('NFD').replace(/[\u0300-\u036f]/g, '')
    // Replace spaces and special chars with underscore
    .replace(/[\s\-]+/g, '_')
    // Remove invalid characters (keep only a-z, A-Z, 0-9, underscore)
    .replace(/[^a-zA-Z0-9_]/g, '')
    // Ensure starts with letter or underscore (prepend underscore if starts with number)
    .replace(/^(\d)/, '_$1')
    // Convert to lowercase (PostgreSQL standard)
    .toLowerCase();
};

// PostgreSQL ENUM Value Normalization (allows more flexibility but still cleans up)
const normalizeEnumValue = (input: string): string => {
  if (!input) return '';
  
  return input
    // Remove accents/diacritics
    .normalize('NFD').replace(/[\u0300-\u036f]/g, '')
    // Trim whitespace from ends
    .trim()
    // Replace multiple spaces with single space
    .replace(/\s+/g, ' ')
    // Remove leading/trailing punctuation that could cause issues
    .replace(/^[\-_]+|[\-_]+$/g, '');
};

interface EnumType {
  name: string;
  schema: string;
  values: string[];
}

interface EnumTypesModalProps {
  isOpen: boolean;
  onClose: () => void;
  enumTypes: EnumType[];
  schemas: string[];
  activeSchema: string;
  onCreate: (name: string, schema: string, values: string[]) => Promise<void>;
  onUpdate: (name: string, schema: string, values: string[]) => Promise<void>;
  onDelete: (name: string, schema: string) => Promise<void>;
  loadingName: string | null;
}

const EnumTypesModal: React.FC<EnumTypesModalProps> = ({
  isOpen,
  onClose,
  enumTypes,
  schemas,
  activeSchema,
  onCreate,
  onUpdate,
  onDelete,
  loadingName
}) => {
  const [search, setSearch] = useState('');
  const [selectedSchema, setSelectedSchema] = useState(activeSchema);
  const [toast, setToast] = useState<{ message: string; type: 'success' | 'error' } | null>(null);
  
  // Create/Edit mode
  const [isEditing, setIsEditing] = useState(false);
  const [editingEnum, setEditingEnum] = useState<EnumType | null>(null);
  const [newEnumName, setNewEnumName] = useState('');
  const [newEnumValues, setNewEnumValues] = useState<string[]>(['']);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState<string | null>(null);
  const [expandedEnum, setExpandedEnum] = useState<string | null>(null);

  useEffect(() => {
    if (isOpen) {
      setSelectedSchema(activeSchema);
      resetForm();
    }
  }, [isOpen, activeSchema]);

  const resetForm = () => {
    setIsEditing(false);
    setEditingEnum(null);
    setNewEnumName('');
    setNewEnumValues(['']);
    setShowDeleteConfirm(null);
    setExpandedEnum(null);
  };

  const filteredEnums = useMemo(() => {
    return enumTypes
      .filter(e => e.schema === selectedSchema)
      .filter(e => {
        const matchesSearch = e.name.toLowerCase().includes(search.toLowerCase()) ||
          e.values.some(v => v.toLowerCase().includes(search.toLowerCase()));
        return matchesSearch;
      })
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [enumTypes, selectedSchema, search]);

  const stats = useMemo(() => {
    const total = enumTypes.length;
    const currentSchema = enumTypes.filter(e => e.schema === selectedSchema).length;
    return { total, currentSchema };
  }, [enumTypes, selectedSchema]);

  const showToast = (message: string, type: 'success' | 'error') => {
    setToast({ message, type });
    setTimeout(() => setToast(null), 4000);
  };

  const handleAddValueField = () => {
    setNewEnumValues([...newEnumValues, '']);
  };

  const handleRemoveValueField = (index: number) => {
    if (newEnumValues.length > 1) {
      setNewEnumValues(newEnumValues.filter((_, i) => i !== index));
    }
  };

  const handleValueChange = (index: number, value: string) => {
    const updated = [...newEnumValues];
    updated[index] = normalizeEnumValue(value);
    setNewEnumValues(updated);
  };

  const handleCreate = async () => {
    if (!newEnumName.trim()) {
      showToast('Nome do ENUM é obrigatório', 'error');
      return;
    }
    
    const validValues = newEnumValues.filter(v => v.trim() !== '');
    if (validValues.length === 0) {
      showToast('Adicione pelo menos um valor', 'error');
      return;
    }

    try {
      await onCreate(newEnumName.trim(), selectedSchema, validValues);
      showToast(`ENUM ${newEnumName} criado com sucesso`, 'success');
      resetForm();
    } catch (err: any) {
      showToast(err.message || 'Erro ao criar ENUM', 'error');
    }
  };

  const handleUpdate = async () => {
    if (!editingEnum) return;
    
    const validValues = newEnumValues.filter(v => v.trim() !== '');
    if (validValues.length === 0) {
      showToast('Adicione pelo menos um valor', 'error');
      return;
    }

    try {
      await onUpdate(editingEnum.name, selectedSchema, validValues);
      showToast(`ENUM ${editingEnum.name} atualizado com sucesso`, 'success');
      resetForm();
    } catch (err: any) {
      showToast(err.message || 'Erro ao atualizar ENUM', 'error');
    }
  };

  const handleDelete = async (enumName: string) => {
    try {
      await onDelete(enumName, selectedSchema);
      showToast(`ENUM ${enumName} excluído com sucesso`, 'success');
      setShowDeleteConfirm(null);
    } catch (err: any) {
      showToast(err.message || 'Erro ao excluir ENUM', 'error');
    }
  };

  const startEdit = (enumType: EnumType) => {
    setIsEditing(true);
    setEditingEnum(enumType);
    setNewEnumName(enumType.name);
    setNewEnumValues(enumType.values);
    setExpandedEnum(null);
  };

  const startCreate = () => {
    resetForm();
    setIsEditing(true);
  };

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[200] flex items-center justify-center p-6 animate-in zoom-in-95">
      <div className="bg-white rounded-[2.5rem] w-full max-w-4xl h-[85vh] shadow-2xl border border-slate-200 flex flex-col overflow-hidden">
        {/* Header */}
        <div className="p-8 border-b border-slate-100 flex justify-between items-center bg-gradient-to-r from-slate-50 to-amber-50/30">
          <div className="flex items-center gap-5">
            <div className="w-14 h-14 bg-gradient-to-br from-amber-500 to-orange-600 text-white rounded-2xl flex items-center justify-center shadow-xl shadow-amber-200">
              <List size={28} />
            </div>
            <div>
              <h3 className="text-2xl font-black text-slate-900 tracking-tight">ENUM Types</h3>
              <div className="flex items-center gap-3 mt-1">
                <span className="text-[10px] font-bold text-amber-600 bg-amber-50 px-2 py-0.5 rounded-full">
                  {stats.currentSchema} no schema
                </span>
                <span className="text-[10px] font-bold text-slate-500 bg-slate-100 px-2 py-0.5 rounded-full">
                  {stats.total} total
                </span>
              </div>
            </div>
          </div>
          <button onClick={onClose} className="p-3 hover:bg-slate-200 rounded-full text-slate-400 transition-colors">
            <X size={24} />
          </button>
        </div>

        {/* Toolbar */}
        <div className="p-6 border-b border-slate-100 flex flex-col md:flex-row gap-4 items-center bg-white">
          <div className="relative flex-1 w-full">
            <Search className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-400" size={18} />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Buscar ENUM types..."
              className="w-full pl-12 pr-4 py-3 bg-slate-50 border border-slate-200 rounded-xl text-sm font-bold outline-none focus:ring-2 focus:ring-amber-500/20 transition-all"
              autoFocus
            />
          </div>
          
          <div className="flex gap-2 w-full md:w-auto">
            <select
              value={selectedSchema}
              onChange={(e) => setSelectedSchema(e.target.value)}
              className="px-4 py-3 bg-slate-50 border border-slate-200 rounded-xl text-sm font-bold outline-none focus:ring-2 focus:ring-amber-500/20"
            >
              {schemas.map(s => (
                <option key={s} value={s}>{s}</option>
              ))}
            </select>
            
            <button
              onClick={startCreate}
              disabled={isEditing}
              className="px-4 py-3 bg-amber-500 hover:bg-amber-600 text-white rounded-xl text-sm font-bold transition-all flex items-center gap-2 disabled:opacity-50"
            >
              <Plus size={18} /> Novo ENUM
            </button>
          </div>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-8 bg-[#FAFBFC] custom-scrollbar">
          {isEditing ? (
            // Create/Edit Form
            <div className="bg-white border border-slate-200 rounded-[2rem] p-6 shadow-sm">
              <div className="flex items-center justify-between mb-6">
                <h4 className="text-lg font-black text-slate-900">
                  {editingEnum ? `Editar ${editingEnum.name}` : 'Criar Novo ENUM'}
                </h4>
                <button onClick={resetForm} className="p-2 hover:bg-slate-100 rounded-xl text-slate-400">
                  <X size={20} />
                </button>
              </div>

              <div className="space-y-4">
                <div>
                  <label className="block text-[10px] font-black uppercase tracking-widest text-slate-500 mb-2">
                    Nome do ENUM
                  </label>
                  <input
                    type="text"
                    value={newEnumName}
                    onChange={(e) => setNewEnumName(normalizePostgresIdentifier(e.target.value))}
                    placeholder="ex: user_status, order_status"
                    disabled={!!editingEnum}
                    className="w-full px-4 py-3 bg-slate-50 border border-slate-200 rounded-xl text-sm font-bold outline-none focus:ring-2 focus:ring-amber-500/20 disabled:bg-slate-100 disabled:text-slate-400"
                  />
                </div>

                <div>
                  <label className="block text-[10px] font-black uppercase tracking-widest text-slate-500 mb-2">
                    Valores
                  </label>
                  <div className="space-y-2">
                    {newEnumValues.map((value, index) => (
                      <div key={index} className="flex gap-2">
                        <div className="flex-1 relative">
                          <Tag className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" size={16} />
                          <input
                            type="text"
                            value={value}
                            onChange={(e) => handleValueChange(index, e.target.value)}
                            placeholder={`Valor ${index + 1}`}
                            className="w-full pl-10 pr-4 py-3 bg-slate-50 border border-slate-200 rounded-xl text-sm font-bold outline-none focus:ring-2 focus:ring-amber-500/20"
                          />
                        </div>
                        {newEnumValues.length > 1 && (
                          <button
                            onClick={() => handleRemoveValueField(index)}
                            className="p-3 text-rose-500 hover:bg-rose-50 rounded-xl transition-all"
                          >
                            <Trash2 size={18} />
                          </button>
                        )}
                      </div>
                    ))}
                  </div>
                  <button
                    onClick={handleAddValueField}
                    className="mt-2 text-sm font-bold text-amber-600 hover:text-amber-700 flex items-center gap-2"
                  >
                    <Plus size={16} /> Adicionar valor
                  </button>
                </div>

                <div className="flex gap-3 pt-4">
                  <button
                    onClick={resetForm}
                    className="flex-1 px-6 py-3 bg-slate-100 hover:bg-slate-200 text-slate-600 rounded-xl text-sm font-bold transition-all"
                  >
                    Cancelar
                  </button>
                  <button
                    onClick={editingEnum ? handleUpdate : handleCreate}
                    disabled={loadingName === (editingEnum?.name || newEnumName)}
                    className="flex-1 px-6 py-3 bg-amber-500 hover:bg-amber-600 text-white rounded-xl text-sm font-bold transition-all flex items-center justify-center gap-2"
                  >
                    {loadingName === (editingEnum?.name || newEnumName) ? (
                      <Loader2 size={18} className="animate-spin" />
                    ) : editingEnum ? (
                      <><CheckCircle2 size={18} /> Salvar Alterações</>
                    ) : (
                      <><Plus size={18} /> Criar ENUM</>
                    )}
                  </button>
                </div>
              </div>
            </div>
          ) : (
            // List View
            <div className="space-y-4">
              {filteredEnums.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-20 text-slate-400">
                  <List size={64} className="opacity-20 mb-4" />
                  <p className="font-black uppercase tracking-widest text-xs">
                    {search ? 'Nenhum ENUM encontrado' : 'Nenhum ENUM neste schema'}
                  </p>
                  <button
                    onClick={startCreate}
                    className="mt-4 px-4 py-2 bg-amber-500 hover:bg-amber-600 text-white rounded-xl text-sm font-bold"
                  >
                    Criar primeiro ENUM
                  </button>
                </div>
              ) : (
                filteredEnums.map(enumType => {
                  const isExpanded = expandedEnum === enumType.name;
                  const isLoading = loadingName === enumType.name;

                  return (
                    <div
                      key={enumType.name}
                      className="bg-white border border-slate-200 rounded-[1.5rem] p-5 transition-all hover:shadow-md"
                    >
                      <div 
                        className="flex items-center justify-between cursor-pointer"
                        onClick={() => setExpandedEnum(isExpanded ? null : enumType.name)}
                      >
                        <div className="flex items-center gap-4">
                          <div className="w-12 h-12 bg-amber-50 text-amber-600 rounded-2xl flex items-center justify-center">
                            <List size={20} />
                          </div>
                          <div>
                            <h4 className="text-lg font-black text-slate-900">{enumType.name}</h4>
                            <p className="text-xs text-slate-500">
                              {enumType.values.length} valor{enumType.values.length !== 1 ? 'es' : ''}
                            </p>
                          </div>
                        </div>
                        
                        <div className="flex items-center gap-2" onClick={(e: React.MouseEvent) => e.stopPropagation()}>
                          <button
                            onClick={() => setExpandedEnum(isExpanded ? null : enumType.name)}
                            className="p-2 hover:bg-slate-100 rounded-xl text-slate-400 transition-all"
                          >
                            {isExpanded ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
                          </button>
                          <button
                            onClick={() => startEdit(enumType)}
                            disabled={isLoading}
                            className="p-2 hover:bg-amber-50 text-slate-400 hover:text-amber-600 rounded-xl transition-all"
                          >
                            <Edit2 size={18} />
                          </button>
                          <button
                            onClick={() => setShowDeleteConfirm(enumType.name)}
                            disabled={isLoading}
                            className="p-2 hover:bg-rose-50 text-slate-400 hover:text-rose-600 rounded-xl transition-all"
                          >
                            <Trash2 size={18} />
                          </button>
                        </div>
                      </div>

                      {isExpanded && (
                        <div className="mt-4 pt-4 border-t border-slate-100">
                          <div className="flex flex-wrap gap-2">
                            {enumType.values.map((v, i) => (
                              <span
                                key={i}
                                className="px-3 py-1.5 bg-slate-100 text-slate-700 rounded-lg text-sm font-bold"
                              >
                                '{v}'
                              </span>
                            ))}
                          </div>
                        </div>
                      )}

                      {/* Delete Confirmation */}
                      {showDeleteConfirm === enumType.name && (
                        <div className="mt-4 p-4 bg-rose-50 border border-rose-200 rounded-xl">
                          <div className="flex items-start gap-3">
                            <AlertTriangle className="text-rose-500 shrink-0" size={20} />
                            <div className="flex-1">
                              <p className="text-sm font-bold text-rose-700">
                                Tem certeza que deseja excluir o ENUM "{enumType.name}"?
                              </p>
                              <p className="text-xs text-rose-600 mt-1">
                                Esta ação não pode ser desfeita. Colunas que usam este tipo serão afetadas.
                              </p>
                            </div>
                          </div>
                          <div className="flex gap-2 mt-3">
                            <button
                              onClick={() => setShowDeleteConfirm(null)}
                              className="px-4 py-2 bg-white text-slate-600 rounded-lg text-xs font-bold"
                            >
                              Cancelar
                            </button>
                            <button
                              onClick={() => handleDelete(enumType.name)}
                              disabled={isLoading}
                              className="px-4 py-2 bg-rose-500 hover:bg-rose-600 text-white rounded-lg text-xs font-bold flex items-center gap-2"
                            >
                              {isLoading ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
                              Excluir
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })
              )}
            </div>
          )}
        </div>

        {/* Toast */}
        {toast && (
          <div className={`absolute bottom-6 left-1/2 -translate-x-1/2 px-6 py-3 rounded-2xl shadow-2xl text-sm font-bold flex items-center gap-2 animate-in slide-in-from-bottom-4 z-[300] ${
            toast.type === 'success' ? 'bg-emerald-600 text-white' : 'bg-rose-600 text-white'
          }`}>
            {toast.type === 'success' ? <CheckCircle2 size={16} /> : <AlertTriangle size={16} />}
            {toast.message}
          </div>
        )}
      </div>
    </div>
  );
};

export default EnumTypesModal;
