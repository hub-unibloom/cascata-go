import React, { useState, useRef, useEffect } from 'react';
import { Plus, X, Braces, Variable, Trash2 } from 'lucide-react';

interface PlusInlineButtonProps {
  onInsert: (value: string) => void;
  disabled?: boolean;
  availableNodes?: string[];
  availableVars?: string[];
}

interface NestedExpression {
  id: string;
  type: 'variable' | 'number' | 'expression';
  value: string;
  operator?: string;
}

export const PlusInlineButton: React.FC<PlusInlineButtonProps> = ({ 
  onInsert, 
  disabled, 
  availableNodes = [],
  availableVars = []
}) => {
  const [isOpen, setIsOpen] = useState(false);
  const [expressions, setExpressions] = useState<NestedExpression[]>([
    { id: '1', type: 'variable', value: '', operator: '+' }
  ]);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const addExpression = () => {
    const newId = (expressions.length + 1).toString();
    setExpressions([...expressions, { 
      id: newId, 
      type: 'variable', 
      value: '', 
      operator: '+' 
    }]);
  };

  const removeExpression = (id: string) => {
    if (expressions.length <= 1) return;
    setExpressions(expressions.filter(e => e.id !== id));
  };

  const updateExpression = (id: string, updates: Partial<NestedExpression>) => {
    setExpressions(expressions.map(e => 
      e.id === id ? { ...e, ...updates } : e
    ));
  };

  const buildExpression = (): string => {
    let result = '';
    expressions.forEach((expr, index) => {
      if (expr.value) {
        if (index > 0 && expr.operator) {
          result += ` ${expr.operator} `;
        }
        if (expr.type === 'expression') {
          result += `(${expr.value})`;
        } else {
          result += expr.value;
        }
      }
    });
    return result || '{{cursor}}';
  };

  const handleApply = () => {
    const expr = buildExpression();
    onInsert(expr);
    setIsOpen(false);
    setExpressions([{ id: '1', type: 'variable', value: '', operator: '+' }]);
  };

  const operators = ['+', '-', '*', '/', '%', '^'];

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setIsOpen(!isOpen)}
        className={`
          flex items-center gap-1 px-2 py-1.5 rounded-lg text-[10px] font-bold
          transition-all duration-200 border
          ${disabled 
            ? 'bg-slate-100 text-slate-400 border-slate-200 cursor-not-allowed' 
            : 'bg-emerald-50 text-emerald-600 border-emerald-200 hover:bg-emerald-100 hover:border-emerald-300'
          }
        `}
      >
        <Plus size={12} />
        <span>Expr</span>
      </button>

      {isOpen && !disabled && (
        <div className="absolute z-50 top-full right-0 mt-1 w-80 bg-white border border-slate-200 rounded-xl shadow-lg overflow-hidden">
          <div className="p-3 space-y-3">
            <div className="flex items-center justify-between">
              <h4 className="text-[11px] font-bold text-slate-700">Expressão Aninhada</h4>
              <button 
                onClick={() => setIsOpen(false)}
                className="text-slate-400 hover:text-slate-600"
              >
                <X size={14} />
              </button>
            </div>

            {/* Expressions List */}
            <div className="space-y-2 max-h-48 overflow-y-auto">
              {expressions.map((expr, index) => (
                <div key={expr.id} className="flex items-center gap-2">
                  {/* Operator (except first) */}
                  {index > 0 && (
                    <select
                      value={expr.operator}
                      onChange={(e) => updateExpression(expr.id, { operator: e.target.value })}
                      className="w-12 bg-slate-50 border border-slate-200 rounded-lg px-1 py-1.5 text-[10px] font-bold text-center"
                    >
                      {operators.map(op => (
                        <option key={op} value={op}>{op}</option>
                      ))}
                    </select>
                  )}

                  {/* Type selector */}
                  <select
                    value={expr.type}
                    onChange={(e) => updateExpression(expr.id, { type: e.target.value as any, value: '' })}
                    className="w-20 bg-slate-50 border border-slate-200 rounded-lg px-2 py-1.5 text-[9px] font-bold"
                  >
                    <option value="variable">Variável</option>
                    <option value="number">Número</option>
                    <option value="expression">Expressão</option>
                  </select>

                  {/* Value input */}
                  {expr.type === 'variable' && (
                    <select
                      value={expr.value}
                      onChange={(e) => updateExpression(expr.id, { value: e.target.value })}
                      className="flex-1 bg-white border border-slate-200 rounded-lg px-2 py-1.5 text-[9px]"
                    >
                      <option value="">Selecionar...</option>
                      {availableNodes.map(node => (
                        <option key={node} value={`{{${node}.data}}`}>{node}</option>
                      ))}
                      {availableVars.map(v => (
                        <option key={v} value={`{{${v}}}`}>{v}</option>
                      ))}
                    </select>
                  )}

                  {expr.type === 'number' && (
                    <input
                      type="number"
                      value={expr.value}
                      onChange={(e) => updateExpression(expr.id, { value: e.target.value })}
                      placeholder="0"
                      className="flex-1 bg-white border border-slate-200 rounded-lg px-2 py-1.5 text-[9px]"
                    />
                  )}

                  {expr.type === 'expression' && (
                    <input
                      type="text"
                      value={expr.value}
                      onChange={(e) => updateExpression(expr.id, { value: e.target.value })}
                      placeholder="(a + b) * c"
                      className="flex-1 bg-white border border-slate-200 rounded-lg px-2 py-1.5 text-[9px] font-mono"
                    />
                  )}

                  {/* Remove button */}
                  {expressions.length > 1 && (
                    <button
                      onClick={() => removeExpression(expr.id)}
                      className="text-slate-300 hover:text-rose-500 transition-colors"
                    >
                      <Trash2 size={12} />
                    </button>
                  )}
                </div>
              ))}
            </div>

            {/* Add button */}
            <button
              onClick={addExpression}
              className="w-full flex items-center justify-center gap-1 py-2 rounded-lg bg-slate-50 hover:bg-slate-100 border border-slate-200 border-dashed text-[9px] font-bold text-slate-600 transition-colors"
            >
              <Plus size={12} />
              Adicionar Termo
            </button>

            {/* Preview */}
            <div className="p-2 bg-slate-50 rounded-lg border border-slate-100">
              <p className="text-[8px] text-slate-500 mb-1">Preview:</p>
              <code className="text-[10px] font-mono text-indigo-600 block truncate">
                {buildExpression()}
              </code>
            </div>

            {/* Actions */}
            <div className="flex gap-2">
              <button
                onClick={handleApply}
                className="flex-1 py-2 rounded-lg bg-indigo-500 hover:bg-indigo-600 text-white text-[10px] font-bold transition-colors"
              >
                Inserir
              </button>
              <button
                onClick={() => setIsOpen(false)}
                className="px-3 py-2 rounded-lg bg-slate-100 hover:bg-slate-200 text-slate-600 text-[10px] font-bold transition-colors"
              >
                Cancelar
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default PlusInlineButton;
