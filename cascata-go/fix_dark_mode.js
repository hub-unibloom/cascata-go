const fs = require('fs');

let file = fs.readFileSync('frontend/pages/ProjectDetail.tsx', 'utf8');

// 1. StatCard
file = file.replace(
  /const colorClasses: Record<string, string> = {[\s\S]*?};/,
  `const colorClasses: Record<string, string> = {
    indigo: "text-indigo-600 bg-indigo-50 border border-indigo-100 group-hover:border-indigo-200 dark:bg-indigo-500/10 dark:text-indigo-400 dark:border-indigo-500/20 dark:group-hover:border-indigo-500/40",
    emerald: "text-emerald-600 bg-emerald-50 border border-emerald-100 group-hover:border-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-400 dark:border-emerald-500/20 dark:group-hover:border-emerald-500/40",
    blue: "text-blue-600 bg-blue-50 border border-blue-100 group-hover:border-blue-200 dark:bg-blue-500/10 dark:text-blue-400 dark:border-blue-500/20 dark:group-hover:border-blue-500/40",
    amber: "text-amber-600 bg-amber-50 border border-amber-100 group-hover:border-amber-200 dark:bg-amber-500/10 dark:text-amber-400 dark:border-amber-500/20 dark:group-hover:border-amber-500/40",
    rose: "text-rose-600 bg-rose-50 border border-rose-100 group-hover:border-rose-200 dark:bg-rose-500/10 dark:text-rose-400 dark:border-rose-500/20 dark:group-hover:border-rose-500/40",
  };`
);

file = file.replace(
  /className="bg-white dark:bg-\[#1A1A1A\] border border-slate-200 dark:border-gray-700 rounded-\[2rem\] p-5 shadow-sm hover:shadow-xl hover:-translate-y-1 transition-all duration-300 group relative overflow-hidden"/g,
  'className="bg-white dark:bg-[#0B101E]/60 dark:backdrop-blur-xl border border-slate-200 dark:border-white/[0.05] rounded-[2rem] p-5 shadow-sm hover:shadow-xl hover:-translate-y-1 transition-all duration-500 group relative overflow-hidden dark:hover:bg-[#111827]/80 dark:hover:border-white/[0.1] dark:hover:shadow-[0_0_40px_rgba(0,0,0,0.3)]"'
);

// StatCard text replacements
file = file.replace(
  /className="text-\[10px\] font-bold text-slate-400 dark:text-gray-400 uppercase tracking-wider leading-tight"/g,
  'className="text-[10px] font-bold text-slate-400 dark:text-slate-400/80 uppercase tracking-wider leading-tight"'
);
file = file.replace(
  /className="text-slate-300 dark:text-gray-600 mx-1"/g,
  'className="text-slate-300 dark:text-white/10 mx-1"'
);
file = file.replace(
  /className="text-slate-500 dark:text-gray-300"/g,
  'className="text-slate-500 dark:text-slate-300/80"'
);

// QuickAction
file = file.replace(
  /bg-white dark:bg-\[#1A1A1A\] border border-slate-100 dark:border-gray-700 hover:border-indigo-200 hover:shadow-lg hover:bg-slate-50 dark:hover:bg-gray-800/g,
  'bg-white dark:bg-[#0B101E]/40 dark:backdrop-blur-lg border border-slate-100 dark:border-white/[0.05] hover:border-indigo-200 hover:shadow-lg hover:bg-slate-50 dark:hover:bg-[#131A2B]/80 dark:hover:border-indigo-500/30 dark:hover:shadow-[0_0_30px_rgba(99,102,241,0.15)]'
);
file = file.replace(
  /bg-slate-100 dark:bg-gray-700 flex items-center justify-center text-slate-500 dark:text-gray-400 group-hover:bg-indigo-600 group-hover:text-white/g,
  'bg-slate-100 dark:bg-white/[0.03] flex items-center justify-center text-slate-500 dark:text-slate-400 group-hover:bg-indigo-600 group-hover:text-white dark:group-hover:bg-indigo-500'
);

// General dark colors
file = file.replace(/dark:bg-\[#1A1A1A\]/g, 'dark:bg-[#0B101E]/60 dark:backdrop-blur-xl');
file = file.replace(/dark:border-gray-700/g, 'dark:border-white/[0.08]');
file = file.replace(/dark:bg-gray-800/g, 'dark:bg-[#131A2B]/90');
file = file.replace(/dark:bg-gray-700/g, 'dark:bg-white/[0.04]');
file = file.replace(/dark:text-gray-400/g, 'dark:text-slate-400');
file = file.replace(/dark:text-gray-300/g, 'dark:text-slate-300');
file = file.replace(/dark:text-gray-500/g, 'dark:text-slate-500');
file = file.replace(/dark:text-gray-600/g, 'dark:text-slate-600');
file = file.replace(/dark:text-gray-200/g, 'dark:text-slate-200');
file = file.replace(/dark:border-gray-600/g, 'dark:border-white/[0.1]');

// Traffic Pulse special chart replacements
file = file.replace(
  /absolute -top-20 -right-20 w-64 h-64 bg-indigo-500\/5 rounded-full blur-3xl group-hover:bg-indigo-500\/10/g,
  'absolute -top-20 -right-20 w-64 h-64 bg-indigo-500/10 dark:bg-indigo-500/20 rounded-full blur-3xl group-hover:bg-indigo-500/20 dark:group-hover:bg-indigo-500/30'
);
file = file.replace(
  /absolute -bottom-20 -left-20 w-64 h-64 bg-rose-500\/5 rounded-full blur-3xl group-hover:bg-rose-500\/10/g,
  'absolute -bottom-20 -left-20 w-64 h-64 bg-rose-500/10 dark:bg-rose-500/20 rounded-full blur-3xl group-hover:bg-rose-500/20 dark:group-hover:bg-rose-500/30'
);

// Tooltips and Glass modals
file = file.replace(
  /bg-white\/95 dark:bg-gray-800\/95 backdrop-blur-sm border border-slate-200 dark:border-gray-600/g,
  'bg-white/95 dark:bg-[#0B101E]/95 backdrop-blur-xl border border-slate-200 dark:border-white/[0.1] shadow-[0_8px_32px_rgba(0,0,0,0.5)]'
);

file = file.replace(
  /bg-white\/90 dark:bg-gray-800\/90/g,
  'bg-white/90 dark:bg-[#0B101E]/80'
);

file = file.replace(
  /bg-slate-50 dark:bg-[#131A2B]\/90/g,
  'bg-slate-50 dark:bg-[#0B101E]/40 dark:backdrop-blur-lg'
);

// Main wrapper
file = file.replace(
  /className="pt-4 lg:pt-6 px-8 lg:px-12 max-w-\[1920px\] mx-auto w-full space-y-8 pb-40 dark"/g,
  'className="pt-4 lg:pt-6 px-8 lg:px-12 max-w-[1920px] mx-auto w-full space-y-8 pb-40"'
);

// Timezone Pills
file = file.replace(
  /bg-indigo-50 dark:bg-indigo-900\/30 border border-indigo-100 dark:border-indigo-700/g,
  'bg-indigo-50 dark:bg-indigo-500/10 border border-indigo-100 dark:border-indigo-500/20'
);
file = file.replace(
  /bg-amber-50 dark:bg-amber-900\/30 border-amber-200 dark:border-amber-700/g,
  'bg-amber-50 dark:bg-amber-500/10 border-amber-200 dark:border-amber-500/20'
);
file = file.replace(
  /bg-rose-50\/90 dark:bg-rose-900\/50 border-rose-100 dark:border-rose-700/g,
  'bg-rose-50/90 dark:bg-rose-500/10 border-rose-100 dark:border-rose-500/20'
);
file = file.replace(
  /bg-emerald-50\/90 dark:bg-emerald-900\/50 border-emerald-100 dark:border-emerald-700/g,
  'bg-emerald-50/90 dark:bg-emerald-500/10 border-emerald-100 dark:border-emerald-500/20'
);


fs.writeFileSync('frontend/pages/ProjectDetail.tsx', file, 'utf8');
console.log('Replacements done in ProjectDetail.tsx');
