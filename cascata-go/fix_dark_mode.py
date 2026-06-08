import re

with open('frontend/pages/ProjectDetail.tsx', 'r') as f:
    content = f.read()

# 1. StatCard
content = re.sub(
    r'const colorClasses: Record<string, string> = \{[\s\S]*?\};',
    '''const colorClasses: Record<string, string> = {
    indigo: "text-indigo-600 bg-indigo-50 border border-indigo-100 group-hover:border-indigo-200 dark:bg-indigo-500/10 dark:text-indigo-400 dark:border-indigo-500/20 dark:group-hover:border-indigo-500/40",
    emerald: "text-emerald-600 bg-emerald-50 border border-emerald-100 group-hover:border-emerald-200 dark:bg-emerald-500/10 dark:text-emerald-400 dark:border-emerald-500/20 dark:group-hover:border-emerald-500/40",
    blue: "text-blue-600 bg-blue-50 border border-blue-100 group-hover:border-blue-200 dark:bg-blue-500/10 dark:text-blue-400 dark:border-blue-500/20 dark:group-hover:border-blue-500/40",
    amber: "text-amber-600 bg-amber-50 border border-amber-100 group-hover:border-amber-200 dark:bg-amber-500/10 dark:text-amber-400 dark:border-amber-500/20 dark:group-hover:border-amber-500/40",
    rose: "text-rose-600 bg-rose-50 border border-rose-100 group-hover:border-rose-200 dark:bg-rose-500/10 dark:text-rose-400 dark:border-rose-500/20 dark:group-hover:border-rose-500/40",
  };''',
    content
)

content = content.replace(
    'className="bg-white dark:bg-[#1A1A1A] border border-slate-200 dark:border-gray-700 rounded-[2rem] p-5 shadow-sm hover:shadow-xl hover:-translate-y-1 transition-all duration-300 group relative overflow-hidden"',
    'className="bg-white dark:bg-[#0B101E]/60 dark:backdrop-blur-xl border border-slate-200 dark:border-white/[0.05] rounded-[2rem] p-5 shadow-sm hover:shadow-xl hover:-translate-y-1 transition-all duration-500 group relative overflow-hidden dark:hover:bg-[#111827]/80 dark:hover:border-white/[0.1] dark:hover:shadow-[0_0_40px_rgba(0,0,0,0.3)]"'
)

# StatCard text replacements
content = content.replace(
    'className="text-[10px] font-bold text-slate-400 dark:text-gray-400 uppercase tracking-wider leading-tight"',
    'className="text-[10px] font-bold text-slate-400 dark:text-slate-400/80 uppercase tracking-wider leading-tight"'
)
content = content.replace(
    'className="text-slate-300 dark:text-gray-600 mx-1"',
    'className="text-slate-300 dark:text-white/10 mx-1"'
)
content = content.replace(
    'className="text-slate-500 dark:text-gray-300"',
    'className="text-slate-500 dark:text-slate-300/80"'
)

# QuickAction
content = content.replace(
    'bg-white dark:bg-[#1A1A1A] border border-slate-100 dark:border-gray-700 hover:border-indigo-200 hover:shadow-lg hover:bg-slate-50 dark:hover:bg-gray-800',
    'bg-white dark:bg-[#0B101E]/40 dark:backdrop-blur-lg border border-slate-100 dark:border-white/[0.05] hover:border-indigo-200 hover:shadow-lg hover:bg-slate-50 dark:hover:bg-[#131A2B]/80 dark:hover:border-indigo-500/30 dark:hover:shadow-[0_0_30px_rgba(99,102,241,0.15)]'
)
content = content.replace(
    'bg-slate-100 dark:bg-gray-700 flex items-center justify-center text-slate-500 dark:text-gray-400 group-hover:bg-indigo-600 group-hover:text-white',
    'bg-slate-100 dark:bg-white/[0.03] flex items-center justify-center text-slate-500 dark:text-slate-400 group-hover:bg-indigo-600 group-hover:text-white dark:group-hover:bg-indigo-500'
)

# General dark colors
content = content.replace('dark:bg-[#1A1A1A]', 'dark:bg-[#0B101E]/60 dark:backdrop-blur-xl')
content = content.replace('dark:border-gray-700', 'dark:border-white/[0.08]')
content = content.replace('dark:bg-gray-800', 'dark:bg-[#131A2B]/90')
content = content.replace('dark:bg-gray-700', 'dark:bg-white/[0.04]')
content = content.replace('dark:text-gray-400', 'dark:text-slate-400')
content = content.replace('dark:text-gray-300', 'dark:text-slate-300')
content = content.replace('dark:text-gray-500', 'dark:text-slate-500')
content = content.replace('dark:text-gray-600', 'dark:text-slate-600')
content = content.replace('dark:text-gray-200', 'dark:text-slate-200')
content = content.replace('dark:border-gray-600', 'dark:border-white/[0.1]')

# Traffic Pulse special chart replacements
content = content.replace(
    'absolute -top-20 -right-20 w-64 h-64 bg-indigo-500/5 rounded-full blur-3xl group-hover:bg-indigo-500/10',
    'absolute -top-20 -right-20 w-64 h-64 bg-indigo-500/10 dark:bg-indigo-500/20 rounded-full blur-3xl group-hover:bg-indigo-500/20 dark:group-hover:bg-indigo-500/30'
)
content = content.replace(
    'absolute -bottom-20 -left-20 w-64 h-64 bg-rose-500/5 rounded-full blur-3xl group-hover:bg-rose-500/10',
    'absolute -bottom-20 -left-20 w-64 h-64 bg-rose-500/10 dark:bg-rose-500/20 rounded-full blur-3xl group-hover:bg-rose-500/20 dark:group-hover:bg-rose-500/30'
)

# Tooltips and Glass modals
content = content.replace(
    'bg-white/95 dark:bg-gray-800/95 backdrop-blur-sm border border-slate-200 dark:border-gray-600',
    'bg-white/95 dark:bg-[#0B101E]/95 backdrop-blur-xl border border-slate-200 dark:border-white/[0.1] shadow-[0_8px_32px_rgba(0,0,0,0.5)]'
)
content = content.replace(
    'bg-white/90 dark:bg-gray-800/90',
    'bg-white/90 dark:bg-[#0B101E]/80'
)
content = content.replace(
    'bg-slate-50 dark:bg-[#131A2B]/90',
    'bg-slate-50 dark:bg-[#0B101E]/40 dark:backdrop-blur-lg'
)

# Main wrapper remove hardcoded dark if I want it controlled globally, but for now let's leave it or remove it?
# The user might be forcing dark mode manually. Let's fix the dark mode colors first.

# Timezone Pills
content = content.replace(
    'bg-indigo-50 dark:bg-indigo-900/30 border border-indigo-100 dark:border-indigo-700',
    'bg-indigo-50 dark:bg-indigo-500/10 border border-indigo-100 dark:border-indigo-500/20'
)
content = content.replace(
    'bg-amber-50 dark:bg-amber-900/30 border-amber-200 dark:border-amber-700',
    'bg-amber-50 dark:bg-amber-500/10 border-amber-200 dark:border-amber-500/20'
)
content = content.replace(
    'bg-rose-50/90 dark:bg-rose-900/50 border-rose-100 dark:border-rose-700',
    'bg-rose-50/90 dark:bg-rose-500/10 border-rose-100 dark:border-rose-500/20'
)
content = content.replace(
    'bg-emerald-50/90 dark:bg-emerald-900/50 border-emerald-100 dark:border-emerald-700',
    'bg-emerald-50/90 dark:bg-emerald-500/10 border-emerald-100 dark:border-emerald-500/20'
)

with open('frontend/pages/ProjectDetail.tsx', 'w') as f:
    f.write(content)

print('Done')
