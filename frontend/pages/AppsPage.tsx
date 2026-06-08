import React from 'react';
import { CreditCard, Search, Database, Cloud, Zap, Shield, Globe, Code2, Layout, Smartphone, Server, Lock, Mail, MessageSquare, Calendar, FileText, Image, Video, Music, Box, Package, ShoppingCart, Users, BarChart, Settings, CheckCircle, Clock, Star } from 'lucide-react';

interface App {
  id: string;
  name: string;
  description: string;
  icon: React.ReactElement;
  category: string;
  status: 'installed' | 'available' | 'popular';
  version?: string;
}

const AppsPage: React.FC = () => {
  const apps: App[] = [
    {
      id: 'stripe',
      name: 'Stripe',
      description: 'Payment processing and subscription management',
      icon: <CreditCard />,
      category: 'Payments',
      status: 'popular',
      version: '14.0.0'
    },
    {
      id: 'typesense',
      name: 'Typesense',
      description: 'Fast, typo-tolerant search engine',
      icon: <Search />,
      category: 'Search',
      status: 'popular',
      version: '0.25.0'
    },
    {
      id: 'meilisearch',
      name: 'Meilisearch',
      description: 'Lightning-fast search engine',
      icon: <Search />,
      category: 'Search',
      status: 'available',
      version: '1.5.0'
    },
    {
      id: 'supabase',
      name: 'Supabase',
      description: 'Open source Firebase alternative',
      icon: <Database />,
      category: 'Database',
      status: 'popular',
      version: '2.0.0'
    },
    {
      id: 'cloudflare',
      name: 'Cloudflare',
      description: 'CDN, security, and edge computing',
      icon: <Cloud />,
      category: 'Infrastructure',
      status: 'available',
      version: '4.0.0'
    },
    {
      id: 'auth0',
      name: 'Auth0',
      description: 'Authentication and authorization platform',
      icon: <Shield />,
      category: 'Auth',
      status: 'popular',
      version: '3.0.0'
    },
    {
      id: 'vercel',
      name: 'Vercel',
      description: 'Cloud platform for frontend developers',
      icon: <Globe />,
      category: 'Hosting',
      status: 'available',
      version: '2.0.0'
    },
    {
      id: 'github',
      name: 'GitHub',
      description: 'Version control and collaboration',
      icon: <Code2 />,
      category: 'Development',
      status: 'installed',
      version: '1.0.0'
    },
    {
      id: 'netlify',
      name: 'Netlify',
      description: 'Modern web development platform',
      icon: <Layout />,
      category: 'Hosting',
      status: 'available',
      version: '3.0.0'
    },
    {
      id: 'twilio',
      name: 'Twilio',
      description: 'Communication APIs for SMS, voice, and video',
      icon: <Smartphone />,
      category: 'Communication',
      status: 'available',
      version: '5.0.0'
    },
    {
      id: 'aws',
      name: 'AWS',
      description: 'Amazon Web Services cloud platform',
      icon: <Server />,
      category: 'Infrastructure',
      status: 'popular',
      version: '2.0.0'
    },
    {
      id: 'sendgrid',
      name: 'SendGrid',
      description: 'Email delivery and API service',
      icon: <Mail />,
      category: 'Communication',
      status: 'available',
      version: '4.0.0'
    },
    {
      id: 'slack',
      name: 'Slack',
      description: 'Team communication and collaboration',
      icon: <MessageSquare />,
      category: 'Communication',
      status: 'installed',
      version: '2.0.0'
    },
    {
      id: 'google-calendar',
      name: 'Google Calendar',
      description: 'Calendar and scheduling integration',
      icon: <Calendar />,
      category: 'Productivity',
      status: 'available',
      version: '1.0.0'
    },
    {
      id: 'notion',
      name: 'Notion',
      description: 'All-in-one workspace for notes and docs',
      icon: <FileText />,
      category: 'Productivity',
      status: 'available',
      version: '3.0.0'
    },
    {
      id: 'unsplash',
      name: 'Unsplash',
      description: 'Free high-quality photos',
      icon: <Image />,
      category: 'Media',
      status: 'available',
      version: '1.0.0'
    },
    {
      id: 'vimeo',
      name: 'Vimeo',
      description: 'Video hosting and streaming',
      icon: <Video />,
      category: 'Media',
      status: 'available',
      version: '2.0.0'
    },
    {
      id: 'spotify',
      name: 'Spotify',
      description: 'Music streaming integration',
      icon: <Music />,
      category: 'Media',
      status: 'available',
      version: '1.0.0'
    },
    {
      id: 'docker',
      name: 'Docker',
      description: 'Container platform for applications',
      icon: <Box />,
      category: 'Infrastructure',
      status: 'popular',
      version: '4.0.0'
    },
    {
      id: 'kubernetes',
      name: 'Kubernetes',
      description: 'Container orchestration platform',
      icon: <Package />,
      category: 'Infrastructure',
      status: 'available',
      version: '1.28.0'
    },
    {
      id: 'shopify',
      name: 'Shopify',
      description: 'E-commerce platform',
      icon: <ShoppingCart />,
      category: 'E-commerce',
      status: 'popular',
      version: '3.0.0'
    },
    {
      id: 'intercom',
      name: 'Intercom',
      description: 'Customer messaging and support',
      icon: <MessageSquare />,
      category: 'Communication',
      status: 'available',
      version: '2.0.0'
    },
    {
      id: 'segment',
      name: 'Segment',
      description: 'Customer data platform',
      icon: <BarChart />,
      category: 'Analytics',
      status: 'available',
      version: '3.0.0'
    },
    {
      id: 'datadog',
      name: 'Datadog',
      description: 'Monitoring and analytics platform',
      icon: <BarChart />,
      category: 'Analytics',
      status: 'available',
      version: '4.0.0'
    }
  ];

  const categories = [...new Set(apps.map(app => app.category))];

  const getStatusBadge = (status: App['status']) => {
    switch (status) {
      case 'installed':
        return (
          <span className="inline-flex items-center gap-1 px-2 py-1 rounded-full bg-emerald-100 text-emerald-700 text-xs font-semibold">
            <CheckCircle size={12} />
            Installed
          </span>
        );
      case 'popular':
        return (
          <span className="inline-flex items-center gap-1 px-2 py-1 rounded-full bg-amber-100 text-amber-700 text-xs font-semibold">
            <Star size={12} />
            Popular
          </span>
        );
      default:
        return (
          <span className="inline-flex items-center gap-1 px-2 py-1 rounded-full bg-slate-100 text-slate-600 text-xs font-semibold">
            Available
          </span>
        );
    }
  };

  return (
    <div className="min-h-screen bg-slate-50">
      {/* Header */}
      <div className="bg-white border-b border-slate-200">
        <div className="max-w-7xl mx-auto px-6 py-8">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-3xl font-bold text-slate-900">Apps Marketplace</h1>
              <p className="text-slate-500 mt-2">Discover and integrate powerful applications into your projects</p>
            </div>
            <div className="flex items-center gap-3">
              <div className="relative">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" size={18} />
                <input
                  type="text"
                  placeholder="Search apps..."
                  className="pl-10 pr-4 py-2.5 border border-slate-200 rounded-xl w-64 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent text-sm"
                />
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Content */}
      <div className="max-w-7xl mx-auto px-6 py-8">
        {categories.map((category) => {
          const categoryApps = apps.filter(app => app.category === category);
          return (
            <div key={category} className="mb-10">
              <h2 className="text-xl font-bold text-slate-900 mb-4 flex items-center gap-2">
                <Settings size={20} className="text-indigo-600" />
                {category}
              </h2>
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                {categoryApps.map((app) => (
                  <div
                    key={app.id}
                    className="bg-white rounded-xl border border-slate-200 p-5 hover:shadow-lg hover:border-indigo-300 transition-all cursor-pointer group"
                  >
                    <div className="flex items-start justify-between mb-3">
                      <div className="w-12 h-12 bg-gradient-to-br from-indigo-500 to-purple-600 rounded-xl flex items-center justify-center text-white shadow-lg">
                        {React.cloneElement(app.icon, { size: 24 })}
                      </div>
                      {getStatusBadge(app.status)}
                    </div>
                    <h3 className="font-semibold text-slate-900 text-lg mb-1 group-hover:text-indigo-600 transition-colors">
                      {app.name}
                    </h3>
                    <p className="text-slate-500 text-sm mb-3 line-clamp-2">{app.description}</p>
                    {app.version && (
                      <div className="flex items-center gap-2 text-xs text-slate-400">
                        <Clock size={12} />
                        v{app.version}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default AppsPage;
