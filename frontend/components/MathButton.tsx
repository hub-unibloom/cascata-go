import React, { useState, useRef, useEffect } from 'react';
import { Calculator, ChevronDown, Plus, Minus, X, Divide, Percent, Power, Sigma, Triangle } from 'lucide-react';

interface MathButtonProps {
  onInsert: (value: string) => void;
  disabled?: boolean;
}

// Math operations organized by category
const MATH_OPERATIONS = {
  basic: [
    { symbol: '+', label: 'Soma', example: 'a + b' },
    { symbol: '-', label: 'Subtração', example: 'a - b' },
    { symbol: '*', label: 'Multiplicação', example: 'a * b' },
    { symbol: '/', label: 'Divisão', example: 'a / b' },
    { symbol: '%', label: 'Módulo', example: 'a % b' },
    { symbol: '^', label: 'Potência', example: 'a ^ b' },
  ],
  functions: [
    { symbol: 'sqrt', label: 'Raiz Quadrada', example: 'sqrt(a)' },
    { symbol: 'abs', label: 'Valor Absoluto', example: 'abs(a)' },
    { symbol: 'round', label: 'Arredondar', example: 'round(a)' },
    { symbol: 'floor', label: 'Arredondar Baixo', example: 'floor(a)' },
    { symbol: 'ceil', label: 'Arredondar Cima', example: 'ceil(a)' },
  ],
  trig: [
    { symbol: 'sin', label: 'Seno', example: 'sin(a)' },
    { symbol: 'cos', label: 'Cosseno', example: 'cos(a)' },
    { symbol: 'tan', label: 'Tangente', example: 'tan(a)' },
  ],
  log: [
    { symbol: 'log', label: 'Logaritmo Base 10', example: 'log(a)' },
    { symbol: 'ln', label: 'Logaritmo Natural', example: 'ln(a)' },
  ],
  advanced: [
    { symbol: 'min', label: 'Mínimo', example: 'min(a, b)' },
    { symbol: 'max', label: 'Máximo', example: 'max(a, b)' },
    { symbol: 'pow', label: 'Potência (função)', example: 'pow(a, b)' },
    { symbol: 'sign', label: 'Sinal', example: 'sign(a)' },
    { symbol: 'trunc', label: 'Truncar', example: 'trunc(a)' },
  ],
};

export const MathButton: React.FC<MathButtonProps> = ({ onInsert, disabled }) => {
  const [isOpen, setIsOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<'basic' | 'functions' | 'trig' | 'log' | 'advanced'>('basic');
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

  const handleSelect = (op: { symbol: string; label: string; example: string }) => {
    // Insert appropriate template based on the operation
    let template = '';
    if (op.example.includes('(a, b)')) {
      template = `${op.symbol}({{cursor}}, {{cursor}})`;
    } else if (op.example.includes('(a)')) {
      template = `${op.symbol}({{cursor}})`;
    } else {
      template = `{{cursor}} ${op.symbol} {{cursor}}`;
    }
    onInsert(template);
    setIsOpen(false);
  };

  const tabs = [
    { key: 'basic', label: 'Básico', icon: <Sigma size={14} /> },
    { key: 'functions', label: 'Funções', icon: <Calculator size={14} /> },
    { key: 'trig', label: 'Trig', icon: <Triangle size={14} /> },
    { key: 'log', label: 'Log', icon: <Divide size={14} /> },
    { key: 'advanced', label: 'Avançado', icon: <Power size={14} /> },
  ];

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
            : 'bg-indigo-50 text-indigo-600 border-indigo-200 hover:bg-indigo-100 hover:border-indigo-300'
          }
        `}
      >
        <Calculator size={12} />
        <span>Math</span>
        <ChevronDown size={10} className={`transition-transform ${isOpen ? 'rotate-180' : ''}`} />
      </button>

      {isOpen && !disabled && (
        <div className="absolute z-50 top-full right-0 mt-1 w-64 bg-white border border-slate-200 rounded-xl shadow-lg overflow-hidden">
          {/* Tabs */}
          <div className="flex border-b border-slate-100">
            {tabs.map((tab) => (
              <button
                key={tab.key}
                onClick={() => setActiveTab(tab.key as any)}
                className={`
                  flex-1 flex items-center justify-center gap-1 py-2 text-[9px] font-bold
                  transition-colors
                  ${activeTab === tab.key 
                    ? 'bg-indigo-50 text-indigo-600 border-b-2 border-indigo-500' 
                    : 'text-slate-500 hover:bg-slate-50'
                  }
                `}
              >
                {tab.icon}
                <span className="hidden sm:inline">{tab.label}</span>
              </button>
            ))}
          </div>

          {/* Operations Grid */}
          <div className="p-2 grid grid-cols-3 gap-1 max-h-48 overflow-y-auto">
            {MATH_OPERATIONS[activeTab].map((op) => (
              <button
                key={op.symbol}
                onClick={() => handleSelect(op)}
                className="
                  flex flex-col items-center justify-center p-2 rounded-lg
                  bg-slate-50 hover:bg-indigo-50 border border-slate-100 hover:border-indigo-200
                  transition-all duration-150 group
                "
                title={op.label}
              >
                <span className="text-lg font-bold text-slate-700 group-hover:text-indigo-600">
                  {op.symbol === 'sqrt' ? '√' : 
                   op.symbol === 'abs' ? '|x|' :
                   op.symbol === 'pow' ? 'xⁿ' : op.symbol}
                </span>
                <span className="text-[8px] text-slate-500 group-hover:text-indigo-500 mt-0.5">
                  {op.label}
                </span>
              </button>
            ))}
          </div>

          {/* Help Text */}
          <div className="px-3 py-2 bg-slate-50 border-t border-slate-100">
            <p className="text-[8px] text-slate-500 text-center">
              Clique para inserir. Use {'{{'}var{'}'} para variáveis.
            </p>
          </div>
        </div>
      )}
    </div>
  );
};

export default MathButton;
