import React, { useState, useEffect, useMemo, useRef } from 'react';
import {
    BookOpen, Copy, Globe, Database, Code2,
    ChevronRight, ChevronDown, Loader2, FileText,
    Sparkles, Plus, Search, Check, Terminal, Download,
    FileJson, Package, Blocks, Link as LinkIcon, Zap,
    Layers, ArrowRight, ShieldCheck, Play, Key, AlertCircle, RefreshCw,
    ListFilter, MousePointer2, CheckSquare, Users, Lock, Fingerprint,
    HardDrive, Upload as UploadIcon, Trash2, Cloud, Radio, Share2, GripVertical,
    Table as TableIcon, Mail
} from 'lucide-react';
import { marked } from 'marked';



interface APIDocsProps {
    projectId: string;
    currentEnv?: string;
}

const SYSTEM_RPC_PREFIXES = ['uuid_', 'pg_', 'armor', 'crypt', 'digest', 'hmac', 'gen_', 'encrypt', 'decrypt', 'pissh_', 'notify_', 'dearmor'];

const normalizeStepUpFactor = (factor: string) => {
    const f = String(factor || '').trim().toLowerCase();
    if (f === 'totp/mfa' || f === 'mfa') return 'totp';
    if (f === 'email_otp' || f === 'email') return 'otp';
    if (f === 'biometria') return 'passkey';
    return f;
};

const tableMethodMatchesHttp = (tableMethod: string, httpMethod: string) => {
    const m = String(tableMethod || '').trim().toLowerCase();
    const http = httpMethod.toUpperCase();
    if ((m === 'read' || m === 'select') && http === 'GET') return true;
    if ((m === 'create' || m === 'insert') && http === 'POST') return true;
    if ((m === 'update' || m === 'patch' || m === 'put') && (http === 'PATCH' || http === 'PUT')) return true;
    if ((m === 'delete' || m === 'remove') && http === 'DELETE') return true;
    if ((m === 'write' || m === 'mutation' || m === 'mutate') && ['POST', 'PATCH', 'PUT', 'DELETE'].includes(http)) return true;
    if (m === 'crud' || m === 'all' || m === '*') return true;
    return false;
};

const getStepUpCodeField = (factors: string[] = []) => {
    const normalized = factors.map(normalizeStepUpFactor).filter(Boolean);
    const preferred = ['totp', 'otp', 'passkey'].find(f => normalized.includes(f)) || normalized[0] || 'totp';
    return {
        provider: preferred,
        key: `${preferred}_code`,
        value: preferred === 'passkey' ? 'passkey_assertion_code' : '123456',
    };
};

const getTableStepUpFactorsForMethod = (metadata: any, method: string): string[] => {
    const tableSecurity = metadata?.tableSecurity;
    if (!tableSecurity) return [];
    const methods = Array.isArray(tableSecurity.methods)
        ? tableSecurity.methods
        : Array.isArray(tableSecurity.operations)
            ? tableSecurity.operations
            : [];
    if (!methods.some((m: string) => tableMethodMatchesHttp(m, method))) return [];
    const factors = Array.isArray(tableSecurity.type)
        ? tableSecurity.type
        : Array.isArray(tableSecurity.allowed_factors)
            ? tableSecurity.allowed_factors
            : [];
    return factors.map(normalizeStepUpFactor).filter(Boolean);
};

// --- STORAGE DEFINITIONS ---
const STORAGE_ENDPOINTS = [
    {
        id: 'storage_sign',
        name: 'Sign Upload (Handshake)',
        method: 'POST',
        path: '/storage/:bucket/sign',
        description: 'Get a presigned URL or upload strategy. Required for S3/Cloud providers.',
        body: { name: "file.png", type: "image/png", size: 1024, path: "folder/subfolder" },
        is_upload: false
    },
    {
        id: 'storage_upload',
        name: 'Upload File (Proxy)',
        method: 'POST',
        path: '/storage/:bucket/upload',
        description: 'Upload file content via multipart/form-data (if Direct strategy unavailable).',
        body: { path: "folder/subfolder" },
        is_upload: true
    },
    {
        id: 'storage_folder',
        name: 'Create Folder',
        method: 'POST',
        path: '/storage/:bucket/folder',
        description: 'Create a virtual directory marker.',
        body: { name: "new_folder", path: "parent_folder" },
        is_upload: false
    },
    {
        id: 'storage_list_buckets',
        name: 'List Buckets',
        method: 'GET',
        path: '/storage/buckets',
        description: 'Retrieve all available storage buckets.',
        body: {},
        is_upload: false
    },
    {
        id: 'storage_list_files',
        name: 'List Files',
        method: 'GET',
        path: '/storage/:bucket/list',
        description: 'List files and folders in a specific bucket.',
        body: {},
        is_upload: false
    },
    {
        id: 'storage_get',
        name: 'Download / Serve',
        method: 'GET',
        path: '/storage/:bucket/object/:path',
        description: 'Retrieve a file via public URL (Secure Proxy).',
        body: {},
        is_upload: false
    },
    {
        id: 'storage_delete',
        name: 'Delete File',
        method: 'DELETE',
        path: '/storage/:bucket/object',
        description: 'Remove a file permanently.',
        body: {},
        is_upload: false
    }
];

// --- REALTIME DEFINITIONS ---
const REALTIME_ENDPOINTS = [
    {
        id: 'realtime_connect',
        name: 'Connect Stream (SSE)',
        method: 'GET',
        path: '/realtime',
        description: 'Subscribe to database changes via Server-Sent Events.',
        body: {},
        params: '?table=users',
        auth_required: false // Uses apikey query param usually
    }
];

// --- VECTOR DEFINITIONS ---
const VECTOR_ENDPOINTS = [
    {
        id: 'vector_search',
        name: 'Semantic Search',
        method: 'POST',
        path: '/vector/points/search',
        description: 'Find similar records using vector embeddings.',
        body: { vector: [0.1, 0.2, 0.3], limit: 5, with_payload: true },
        auth_required: true
    },
    {
        id: 'vector_upsert',
        name: 'Upsert Points',
        method: 'PUT',
        path: '/vector/points',
        description: 'Insert or update vector data points.',
        body: { points: [{ id: "uuid-1", vector: [0.1, 0.2], payload: { text: "hello" } }] },
        auth_required: true
    },
    {
        id: 'vector_delete',
        name: 'Delete Points',
        method: 'POST',
        path: '/vector/points/delete',
        description: 'Remove points by ID.',
        body: { points: ["uuid-1"] },
        auth_required: true
    }
];

// --- FORMAT PRESETS (Synced with Backend & DB Explorer) ---
const FORMAT_PRESETS: Record<string, { label: string; regex: string; example: string; icon: any }> = {
    email: { label: 'Email', regex: '^[a-zA-Z0-9._%+\\-]+@[a-zA-Z0-9.\\-]+\\.[a-zA-Z]{2,}$', example: 'user@example.com', icon: Mail },
    cpf: { label: 'CPF', regex: '^\\d{3}\\.?\\d{3}\\.?\\d{3}-?\\d{2}$', example: '123.456.789-00', icon: Fingerprint },
    cnpj: { label: 'CNPJ', regex: '^\\d{2}\\.?\\d{3}\\.?\\d{3}\\/?\\d{4}-?\\d{2}$', example: '12.345.678/0001-99', icon: ShieldCheck },
    phone_br: { label: 'Phone (BR)', regex: '^\\+?55\\s?\\(?\\d{2}\\)?\\s?\\d{4,5}-?\\d{4}$', example: '+55 (11) 99999-1234', icon: Radio },
    phone_us: { label: 'Phone (US)', regex: '^\\+?1\\s?\\(?\\d{3}\\)?\\s?\\d{3}-?\\d{4}$', example: '+1 (555) 000-1234', icon: Radio },
    phone: { label: 'Phone', regex: '^\\+?[1-9]\\d{1,14}$', example: '+15559998877', icon: Radio },
    cep: { label: 'CEP', regex: '^\\d{5}-?\\d{3}$', example: '01310-100', icon: Globe },
    zip_us: { label: 'Zip Code (US)', regex: '^\\d{5}(-\\d{4})?$', example: '90210', icon: Globe },
    url: { label: 'URL', regex: '^https?:\\/\\/[a-zA-Z0-9\\-]+(\\.[a-zA-Z0-9\\-]+)+(\\/.*)?$', example: 'https://example.com', icon: LinkIcon },
    uuid_format: { label: 'UUID', regex: '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$', example: 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', icon: Key },
    nis: { label: 'NIS/PIS/PASEP', regex: '^\\d{3}-?\\d{2}-?\\d{4}$', example: '627-18-9432', icon: Fingerprint },
    rg: { label: 'RG', regex: '^\\d{1,2}\\.?\\d{3}\\.?\\d{3}-?[\\dX]$', example: '52.234.567-X', icon: Fingerprint },
    date_br: { label: 'Date (BR)', regex: '^\\d{2}\\/\\d{2}\\/\\d{4}$', example: '25/02/2026', icon: Globe },
};

// --- AUTH DEFINITIONS (Static Documentation) ---
const AUTH_ENDPOINTS = [
    {
        id: 'auth_signup',
        name: 'Sign Up',
        method: 'POST',
        path: '/auth/v1/signup',
        description: 'Register a new user with chosen identity strategy.',
        body: { email: "user@example.com", password: "secure_password_123" },
        auth_required: false
    },
    {
        id: 'auth_login',
        name: 'Sign In',
        method: 'POST',
        path: '/auth/v1/token',
        description: 'Log in an existing user to obtain an Access Token.',
        body: { email: "user@example.com", password: "secure_password_123", grant_type: "password" },
        auth_required: false
    },
    {
        id: 'auth_passkey_reg_start',
        name: 'Passkey Register Start',
        method: 'POST',
        path: '/auth/passkey/register/start',
        description: 'Initiate a biometric/passkey registration flow. Generates challenge options for the client browser.',
        body: { username: "user@example.com", displayName: "User Example" },
        auth_required: true
    },
    {
        id: 'auth_passkey_reg_finish',
        name: 'Passkey Register Finish',
        method: 'POST',
        path: '/auth/passkey/register/finish',
        description: 'Verify and complete the biometric/passkey registration with the credential created by the browser.',
        body: {
            id: "AR...vG",
            rawId: "AR...vG",
            type: "public-key",
            response: {
                clientDataJSON: "eyJ0e...NfQ",
                attestationObject: "o2Nhd...dCA"
            }
        },
        auth_required: true
    },
    {
        id: 'auth_passkey_login_start',
        name: 'Passkey Login Start',
        method: 'POST',
        path: '/auth/passkey/login/start',
        description: 'Initiate a biometric/passkey login flow. Generates assertion challenge options for the browser.',
        body: { identifier: "user@example.com" },
        auth_required: false
    },
    {
        id: 'auth_passkey_login_finish',
        name: 'Passkey Login Finish',
        method: 'POST',
        path: '/auth/passkey/login/finish',
        description: 'Verify the biometric credential/assertion signed by the user device and issue an Access Token.',
        body: {
            id: "AR...vG",
            rawId: "AR...vG",
            type: "public-key",
            response: {
                clientDataJSON: "eyJ0e...NfQ",
                authenticatorData: "SZYN...w==",
                signature: "MEQC...A==",
                userHandle: "eXVz...bGU="
            }
        },
        auth_required: false
    },
    {
        id: 'auth_magic',
        name: 'Magic Link / OTP',
        method: 'POST',
        path: '/auth/challenge',
        description: 'Initiate a passwordless flow (Email Magic Link or OTP).',
        body: { provider: "email", identifier: "user@example.com" },
        auth_required: false
    },
    {
        id: 'auth_verify',
        name: 'Verify OTP',
        method: 'POST',
        path: '/auth/verify-challenge',
        description: 'Verify the code sent via Magic Link/OTP flow.',
        body: { provider: "email", identifier: "user@example.com", code: "123456" },
        auth_required: false
    },
    {
        id: 'auth_user',
        name: 'Get User',
        method: 'GET',
        path: '/auth/v1/user',
        description: 'Retrieve details of the currently logged-in user.',
        body: {},
        auth_required: true // Needs Bearer
    },
    {
        id: 'auth_update_user',
        name: 'Update User / Link Password',
        method: 'PUT',
        path: '/auth/v1/user',
        description: 'Update user data or link a native password. Supports custom strategies (e.g. CPF). Se o gestor ativar "require_otp_on_update" no provedor, o envio de `otp_code` é OBRIGATÓRIO (Bank-Grade Lock). Comportamento de disparo OTP configurável via `otp_dispatch_mode` (delegated, auto_current, auto_primary).',
        body: { password: "new_secure_password", provider: "cpf", identifier: "123.456.789-00", otp_code: "582491" },
        auth_required: true // Needs Bearer
    },
    {
        id: 'auth_recover',
        name: 'Recover Password',
        method: 'POST',
        path: '/auth/v1/recover',
        description: 'Send a recovery mechanism. Use provider/identifier for custom logic (like CPF/Phone).',
        body: { identifier: "user@example.com", provider: "email" },
        auth_required: false
    },
    {
        id: 'auth_mfa_enroll',
        name: 'MFA/TOTP Enroll',
        method: 'POST',
        path: '/auth/v1/mfa/enroll',
        description: 'Initiate TOTP (Authenticator App) setup for the logged-in user. Returns a QR code URL and a secret.',
        body: {},
        auth_required: true
    },
    {
        id: 'auth_mfa_verify',
        name: 'MFA/TOTP Verify & Activate',
        method: 'POST',
        path: '/auth/v1/mfa/verify',
        description: 'Verify the TOTP setup code to activate MFA for the logged-in user.',
        body: { code: "123456" },
        auth_required: true
    }
];

// --- REGEX TO REALISTIC VALUE GENERATOR ---
// Generates a valid example from a regex pattern for curl demonstrations
const generateValueFromPattern = (pattern: string, fieldName: string = ''): string => {
    if (!pattern) return 'text_value';

    // Check if pattern matches a known preset
    for (const [key, preset] of Object.entries(FORMAT_PRESETS)) {
        // Normalize patterns for comparison (remove escape differences)
        const normalizedPreset = preset.regex.replace(/\\/g, '');
        const normalizedPattern = pattern.replace(/\\/g, '');
        if (normalizedPattern === normalizedPreset || pattern === preset.regex) {
            return preset.example;
        }
    }

    // Pattern-specific generators for common regex constructs
    const name = fieldName.toLowerCase();

    // NIS/PIS pattern (3 digits - 2 digits - 4 digits)
    if (pattern.includes('\\d{3}') && pattern.includes('\\d{2}') && pattern.includes('\\d{4}')) {
        return '627-18-9432';
    }

    // RG pattern (1-2 digits . 3 digits . 3 digits - digit/X)
    if (pattern.includes('\\d{1,2}') && pattern.includes('\\.?\\d{3}') && pattern.includes('[\\dX]')) {
        return '52.234.567-X';
    }

    // CPF pattern
    if (pattern.includes('\\d{3}') && pattern.includes('\\d{2}$') && name.includes('cpf')) {
        return '123.456.789-00';
    }

    // CNPJ pattern
    if (pattern.includes('\\d{2}') && pattern.includes('\\d{4}') && name.includes('cnpj')) {
        return '12.345.678/0001-99';
    }

    // Email pattern
    if (pattern.includes('@') || pattern.includes('email') || name.includes('email')) {
        return 'user@example.com';
    }

    // Phone pattern
    if ((pattern.includes('\\d{4}') && pattern.includes('\\d{4}$')) || name.includes('phone') || name.includes('tel')) {
        return '+55 (11) 99999-1234';
    }

    // CEP pattern
    if (pattern.includes('\\d{5}') && pattern.includes('\\d{3}')) {
        return '01310-100';
    }

    // UUID pattern
    if (pattern.includes('[0-9a-f]') || pattern.includes('\\w{8}')) {
        return 'a1b2c3d4-e5f6-7890-abcd-ef1234567890';
    }

    // Date patterns
    if (pattern.includes('\\d{2}\\/\\d{2}\\/\\d{4}')) {
        return '25/02/2026';
    }

    // Generic digit patterns - generate based on quantifiers
    const digitMatch = pattern.match(/\\d\{(\d+)(?:,(\d+))?\}/g);
    if (digitMatch) {
        // Extract the first quantifier
        const firstQuant = digitMatch[0].match(/\\d\{(\d+)(?:,(\d+))?\}/);
        if (firstQuant) {
            const min = parseInt(firstQuant[1]);
            const max = firstQuant[2] ? parseInt(firstQuant[2]) : min;
            const len = Math.max(min, Math.min(max, 8)); // Reasonable default
            return Array(len).fill(0).map(() => Math.floor(Math.random() * 10)).join('');
        }
    }

    // Alphanumeric fallback
    if (pattern.includes('[A-Za-z0-9]') || pattern.includes('\\w')) {
        return 'ABC123XYZ';
    }

    // Default: return the pattern itself as a hint (escaped properly)
    return 'text_value';
};

// --- SMART VALUE GENERATOR ---
// Now accepts formatPattern and enumTypes to generate realistic examples
const generateSmartValue = (name: string, type: string, formatPattern?: string, enumTypes?: Record<string, string[]>) => {
    const n = name.toLowerCase();
    const t = type.toLowerCase();

    // PRIORITY 0: Check if type is an ENUM and return first valid value
    if (enumTypes) {
        const enumName = t.split('.').pop() || t; // Handle "public.user_status" -> "user_status"
        const enumValues = enumTypes[enumName] || enumTypes[t];
        if (enumValues && enumValues.length > 0) {
            return enumValues[0]; // Return first enum value as example
        }
    }

    // PRIORITY 1: If we have a format pattern, use it to generate a valid example
    if (formatPattern) {
        const generated = generateValueFromPattern(formatPattern, name);
        if (generated !== 'text_value') return generated;
    }

    // PRIORITY 2: Check field name heuristics for known formats
    if (n.includes('cpf')) return '123.456.789-00';
    if (n.includes('cnpj')) return '12.345.678/0001-99';
    if (n.includes('nis') || n.includes('pis') || n.includes('pasep')) return '627-18-9432';
    if (n.includes('rg')) return '52.234.567-X';
    if (n.includes('cep')) return '01310-100';
    if (n.includes('phone') || n.includes('telefone') || n.includes('celular')) return '+55 (11) 99999-1234';
    if (n.includes('email')) return 'user@example.com';
    if (n.includes('url') || n.includes('site') || n.includes('link')) return 'https://example.com';
    if (n.includes('uuid') || n.includes('guid')) return 'a1b2c3d4-e5f6-7890-abcd-ef1234567890';

    // PRIORITY 3: Type-based fallbacks
    if (t.includes('bool')) return true;

    if (t.includes('int') || t.includes('serial')) {
        if (n === 'id' || n.endsWith('_id')) return 1;
        if (n.includes('limit')) return 10;
        if (n.includes('offset')) return 0;
        if (n.includes('status')) return 1;
        if (n.includes('qty') || n.includes('count') || n.includes('estoque')) return 100;
        return 0;
    }

    if (t.includes('numeric') || t.includes('float') || t.includes('double') || t.includes('decimal') || t.includes('money')) {
        if (n.includes('price') || n.includes('preco') || n.includes('cost') || n.includes('valor')) return 99.90;
        if (n.includes('tax') || n.includes('rate')) return 0.15;
        if (n.includes('weight') || n.includes('peso')) return 1.5;
        return 10.0;
    }

    if (t.includes('timestamp') || t.includes('date') || t.includes('time')) {
        return new Date().toISOString();
    }

    if (t.includes('uuid')) {
        return "550e8400-e29b-41d4-a716-446655440000";
    }

    if (t.includes('json')) return { key: "value", tags: ["a", "b"] };
    if (t.includes('array') || t.startsWith('_')) return ["option1", "option2"];

    if (n.includes('name') || n.includes('nome')) return "Exemplo Nome";

    return "text_value";
};

const DEFAULT_GROUP_ORDER = ['auth', 'realtime', 'vector', 'storage', 'tables', 'edge', 'rpc'];

// --- SMART SEARCH ENGINE ---
const smartSearch = (item: any, query: string): boolean => {
    if (!query) return true;
    const q = query.toLowerCase().trim();

    // For simple string items (table names, function names)
    if (typeof item === 'string') {
        return item.toLowerCase().includes(q);
    }

    // For rich objects (endpoints)
    const searchableText = [
        item.name,
        item.description,
        item.path,
        item.method,
        item.id
    ].filter(Boolean).join(' ').toLowerCase();

    // Check if query is contained in the aggregated text
    return searchableText.includes(q);
};

const APIDocs: React.FC<APIDocsProps> = ({ projectId, currentEnv }) => {
    const [activeTab, setActiveTab] = useState<'reference' | 'connect' | 'guides'>('reference');
    const [routingStyle, setRoutingStyle] = useState<'sovereign' | 'legacy'>('sovereign');
    const [specFormat, setSpecFormat] = useState<'openapi3' | 'swagger2'>('openapi3');
    const [spec, setSpec] = useState<any>(null);
    const [guides, setGuides] = useState<any[]>([]);
    const [customFunctions, setCustomFunctions] = useState<any[]>([]);
    const [buckets, setBuckets] = useState<any[]>([]);
    const [selectedGuide, setSelectedGuide] = useState<any>(null);
    const [loading, setLoading] = useState(true);
    const [projectData, setProjectData] = useState<any>(null);
    const [authStrategies, setAuthStrategies] = useState<any>(null);

    // UX States
    const [expandedItems, setExpandedItems] = useState<Set<string>>(new Set());
    const [expandedParams, setExpandedParams] = useState<Set<string>>(new Set());

    // Sidebar Accordion State (Default all closed)
    const [expandedSidebarGroups, setExpandedSidebarGroups] = useState<Set<string>>(new Set());

    // Drag & Drop Order State
    const [groupOrder, setGroupOrder] = useState<string[]>(() => {
        try {
            const saved = localStorage.getItem('cascata_docs_order');
            return saved ? JSON.parse(saved) : DEFAULT_GROUP_ORDER;
        } catch { return DEFAULT_GROUP_ORDER; }
    });
    const [draggedGroup, setDraggedGroup] = useState<string | null>(null);

    // Long Press State
    const longPressTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
    const isLongPress = useRef(false);

    // Table Operations State
    const [tableOperations, setTableOperations] = useState<Record<string, string>>({});

    const [searchQuery, setSearchQuery] = useState('');
    const [richMetadata, setRichMetadata] = useState<Record<string, any>>({});
    const [enumTypes, setEnumTypes] = useState<Record<string, string[]>>({}); // name -> values[]

    // Selection Logic States
    const [selectedItems, setSelectedItems] = useState<Set<string>>(new Set());
    const [isMultiSelectMode, setIsMultiSelectMode] = useState(false);

    const [copiedUrl, setCopiedUrl] = useState<string | null>(null);

    const [activeKeyId, setActiveKeyId] = useState<string>('default');

    // Curl format display style: 'angle' (<var>), 'bracket' ([var]), 'double-brace' ({{var}}), 'dollar' (${var}), 'custom', or 'example' (real values)
    const [curlFormat, setCurlFormat] = useState<'angle' | 'bracket' | 'double-brace' | 'dollar' | 'custom' | 'example'>('example');
    const [customPattern, setCustomPattern] = useState<string>('{{%s}}'); // %s will be replaced with field name
    const [clipboardBearer, setClipboardBearer] = useState<string | null>(null); // Bearer value pasted from clipboard when re-clicking 'example'

    // Optional step-up code injection for Sign In / Update User / Recover Password auth endpoints
    const [authStepUpCode, setAuthStepUpCode] = useState<string | null>(null); // null = not injected

    // Execution modal state
    const [execModal, setExecModal] = useState<{
        curl: string;
        status: number | null;
        statusText: string;
        responseBody: string;
        responseHeaders: Record<string, string>;
        loading: boolean;
        error: string | null;
    } | null>(null);

    // Per-card curl overrides (keyed by cardId, set when user edits a CodeBlock inline)
    const [curlOverrides, setCurlOverrides] = useState<Record<string, string>>({});

    // Selected step-up code for OTP/TOTP/Passkey (user can click to change)
    const [selectedStepUpCode, setSelectedStepUpCode] = useState<string>('totp_code');
    const [stepUpCodeValue, setStepUpCodeValue] = useState<string>('123456');

    // Function to read from clipboard and update step-up code value
    const handleStepUpCodeClick = async (code: string) => {
        setSelectedStepUpCode(code);
        try {
            const clipboardText = await navigator.clipboard.readText();
            if (clipboardText) {
                setStepUpCodeValue(clipboardText);
            }
        } catch (err) {
            console.log('Failed to read clipboard:', err);
        }
    };

    // Handler for the optional step-up label on Sign In / Update User / Recover Password.
    // First click on a code word activates it (adds the field to the curl body).
    // Clicking again when already active reads the clipboard and updates the value (same pattern as handleStepUpCodeClick).
    const handleAuthStepUpClick = async (code: string) => {
        if (authStepUpCode === code) {
            // Already active — try to pull a real value from clipboard
            try {
                const clipboardText = navigator.clipboard && window.isSecureContext
                    ? await navigator.clipboard.readText()
                    : window.prompt('Cole o valor do código:') || '';
                if (clipboardText) setStepUpCodeValue(clipboardText.trim());
            } catch (err) {
                console.log('Failed to read clipboard for auth step-up:', err);
            }
        } else {
            setAuthStepUpCode(code);
        }
    };

    // Parse a curl string and execute it as a real fetch, then open the result modal
    const executeCurl = async (curlStr: string) => {
        setExecModal({ curl: curlStr, status: null, statusText: '', responseBody: '', responseHeaders: {}, loading: true, error: null });

        try {
            // Extract method
            const methodMatch = curlStr.match(/-X\s+(\w+)/);
            const method = methodMatch ? methodMatch[1].toUpperCase() : 'GET';

            // Extract URL (first quoted string after curl)
            const urlMatch = curlStr.match(/curl[^"]*"([^"]+)"/);
            if (!urlMatch) throw new Error('URL não encontrada no curl.');
            const url = urlMatch[1];

            // Extract headers
            const headers: Record<string, string> = {};
            const headerMatches = curlStr.matchAll(/-H\s+"([^:]+):\s*([^"]+)"/g);
            for (const m of headerMatches) headers[m[1].trim()] = m[2].trim();

            // Extract body (-d '...' or -d "...")
            const bodyMatch = curlStr.match(/-d\s+'([\s\S]*?)'\s*(?:\\|$)/) || curlStr.match(/-d\s+"([\s\S]*?)"\s*(?:\\|$)/);
            const body = bodyMatch ? bodyMatch[1] : undefined;

            const res = await fetch(url, {
                method,
                headers,
                body: body || undefined,
            });

            const resText = await res.text();
            const resHeaders: Record<string, string> = {};
            res.headers.forEach((v, k) => { resHeaders[k] = v; });

            let prettyBody = resText;
            try { prettyBody = JSON.stringify(JSON.parse(resText), null, 2); } catch {}

            setExecModal(prev => prev ? {
                ...prev,
                loading: false,
                status: res.status,
                statusText: res.statusText,
                responseBody: prettyBody,
                responseHeaders: resHeaders,
            } : null);
        } catch (err: any) {
            setExecModal(prev => prev ? { ...prev, loading: false, error: String(err?.message || err) } : null);
        }
    };
    // read the clipboard and use that value as the Bearer token in all curl examples.
    const handleExampleFormatClick = async () => {
        if (curlFormat === 'example') {
            // navigator.clipboard requires a Secure Context (https or localhost).
            // On plain http (e.g. http://192.168.x.x) the API is undefined, so we
            // fall back to a prompt() so it works regardless of the access scheme.
            if (navigator.clipboard && window.isSecureContext) {
                try {
                    const clipboardText = await navigator.clipboard.readText();
                    if (clipboardText) setClipboardBearer(clipboardText.trim());
                } catch (err) {
                    console.log('Failed to read clipboard for bearer:', err);
                }
            } else {
                const manual = window.prompt('Cole seu Bearer token:');
                if (manual && manual.trim()) setClipboardBearer(manual.trim());
            }
        } else {
            setCurlFormat('example');
            setClipboardBearer(null); // reset when switching back normally
        }
    };

    // Active environment name derived from URL location hash (URL-First) with dynamic fallback
    const activeBranch = (() => {
        const hash = typeof window !== 'undefined' ? window.location.hash : '';
        const match = hash.match(/\/branch\/([^/?]+)/);
        return (match && match[1]) ? match[1] : (currentEnv || 'live');
    })();

    // --- DYNAMIC AUTH STRATEGIES ---
    const dynamicStrategies = useMemo(() => {
        const strategies = authStrategies || projectData?.metadata?.auth_strategies || projectData?.metadata?.extra?.auth_strategies;
        if (!strategies) {
            return [{ id: 'email', name: 'Email', provider: 'email', identifierLabel: 'Email', identifierValue: 'user@example.com', icon: Mail }];
        }
        return Object.entries(strategies)
            .filter(([_, config]: [string, any]) => config.enabled !== false)
            .map(([key, config]: [string, any]) => {
                const lowerKey = key.toLowerCase();
                const regex = config.otp_config?.regex_validation;

                // 1. Exact Regex Match
                let preset = Object.values(FORMAT_PRESETS).find((p: any) => p.regex === regex);

                // 2. Exact Key Match
                if (!preset) preset = FORMAT_PRESETS[key];

                // 3. Heuristic: Key Name Analysis
                if (!preset) {
                    if (lowerKey.includes('phone') || lowerKey.includes('tel')) preset = FORMAT_PRESETS.phone;
                    else if (lowerKey.includes('mail')) preset = FORMAT_PRESETS.email;
                    else if (lowerKey.includes('cpf')) preset = FORMAT_PRESETS.cpf;
                    else if (lowerKey.includes('cnpj')) preset = FORMAT_PRESETS.cnpj;
                    else if (lowerKey.includes('zip') || lowerKey.includes('cep')) preset = FORMAT_PRESETS.cep;
                }

                // 4. Heuristic: Regex Pattern Analysis (Prefix matching)
                if (!preset && regex) {
                    if (regex.startsWith('^\\+?1')) preset = FORMAT_PRESETS.phone_us;
                    else if (regex.startsWith('^\\+?55')) preset = FORMAT_PRESETS.phone_br;
                }

                const finalLabel = preset?.label || key.charAt(0).toUpperCase() + key.slice(1);
                const finalExample = preset?.example || 'your-identifier';
                const finalIcon = preset?.icon || Key;

                return {
                    id: key,
                    name: finalLabel,
                    provider: key,
                    identifierLabel: finalLabel,
                    identifierValue: finalExample,
                    icon: finalIcon
                };
            });
    }, [projectData]);

    const [selectedStrategy, setSelectedStrategy] = useState('email');

    // Ensure selectedStrategy is valid when dynamicStrategies change
    useEffect(() => {
        if (dynamicStrategies.length > 0 && !dynamicStrategies.find((s: any) => s.id === selectedStrategy)) {
            setSelectedStrategy(dynamicStrategies[0].id);
        }
    }, [dynamicStrategies]);

    const getActiveAnonKey = () => {
        if (!projectData) return '<YOUR_ANON_KEY>';
        if (activeKeyId === 'default') return projectData.anon_key;
        const client = projectData.metadata?.app_clients?.find((c: any) => c.id === activeKeyId);
        return client ? client.anon_key : projectData.anon_key;
    };

    // Reset clipboard bearer when the user switches app client key
    useEffect(() => {
        setClipboardBearer(null);
    }, [activeKeyId]);

    const [tablesList, setTablesList] = useState<string[]>([]);
    const [generating, setGenerating] = useState(false);
    const [showGenModal, setShowGenModal] = useState(false);

    useEffect(() => {
        fetchData();
    }, [projectId]);

    // Refetch when spec format or environment changes
    useEffect(() => {
        fetchData();
    }, [specFormat, activeBranch]);

    // Persist Group Order
    useEffect(() => {
        localStorage.setItem('cascata_docs_order', JSON.stringify(groupOrder));
    }, [groupOrder]);

    const fetchData = async () => {
        try {
            const token = localStorage.getItem('cascata_token');
            const headers = { 'Authorization': `Bearer ${token}` };

            // Choose endpoint based on spec format and environment
            const specEndpoint = specFormat === 'swagger2'
                ? `/api/data/${projectId}/rest/v1`
                : `/api/data/${projectId}/docs/openapi`;

            const strategiesUrl = activeBranch && activeBranch !== 'live'
                ? `/api/data/${projectId}/branch/${activeBranch}/auth/strategies`
                : `/api/data/${projectId}/auth/strategies`;

            const [specRes, guidesRes, tablesRes, functionsRes, projectRes, bucketsRes, enumRes, strategiesRes] = await Promise.all([
                fetch(specEndpoint, { headers }),
                fetch(`/api/data/${projectId}/docs/pages`, { headers }),
                fetch(`/api/data/${projectId}/tables`, { headers }),
                fetch(`/api/data/${projectId}/functions`, { headers }),
                fetch('/api/control/projects', { headers }),
                fetch(`/api/data/${projectId}/storage/buckets`, { headers }),
                fetch(`/api/data/${projectId}/enum-types`, { headers }),
                fetch(strategiesUrl, { headers }).catch(() => null)
            ]);

            // Load enum types for smart value generation
            try {
                const enums = await enumRes.json();
                if (Array.isArray(enums)) {
                    const enumMap: Record<string, string[]> = {};
                    enums.forEach((e: any) => {
                        if (e.name && e.values) {
                            enumMap[e.name.toLowerCase()] = e.values;
                        }
                    });
                    setEnumTypes(enumMap);
                }
            } catch (enumErr) {
                console.log('[APIDocs] Failed to load enum types:', enumErr);
            }

            setSpec(await specRes.json());
            const g = await guidesRes.json();
            setGuides(g);
            if (g.length > 0 && !selectedGuide) setSelectedGuide(g[0]);

            const t = await tablesRes.json();
            setTablesList(t.map((r: any) => r.name));
            setRichMetadata(prev => {
                const next = { ...prev };
                if (Array.isArray(t)) {
                    t.forEach((row: any) => {
                        if (!row?.name) return;
                        next[row.name] = {
                            ...(next[row.name] || { type: 'table' }),
                            type: 'table',
                            tableSecurity: {
                                methods: Array.isArray(row.methods) ? row.methods : [],
                                type: Array.isArray(row.type) ? row.type : [],
                            },
                        };
                    });
                }
                return next;
            });

            const f = await functionsRes.json();
            setCustomFunctions(f);

            const b = await bucketsRes.json();
            setBuckets(b);

            const projects = await projectRes.json();
            console.log('[DEBUG APIDocs] Projects data:', projects);
            const current = projects.find((p: any) => p.slug === projectId);
            console.log('[DEBUG APIDocs] Current project:', current);
            console.log('[DEBUG APIDocs] custom_domain:', current?.custom_domain);
            setProjectData(current);

            if (strategiesRes && strategiesRes.ok) {
                try {
                    const strats = await strategiesRes.json();
                    setAuthStrategies(strats);
                } catch (e) {
                    console.error('[APIDocs] Failed to parse strategies:', e);
                }
            }

        } catch (e) {
            console.error("Failed to load docs", e);
        } finally {
            setLoading(false);
        }
    };

    const fetchMetadata = async (name: string, type: 'table' | 'rpc') => {
        if (richMetadata[name]?.fields) return;

        try {
            const token = localStorage.getItem('cascata_token');
            let data = null;

            if (type === 'table') {
                const res = await fetch(`/api/data/${projectId}/tables/${name}/columns`, {
                    headers: { 'Authorization': `Bearer ${token}` }
                });
                if (res.ok) data = await res.json();
            } else {
                const res = await fetch(`/api/data/${projectId}/rpc/${name}/definition`, {
                    headers: { 'Authorization': `Bearer ${token}` }
                });
                if (res.ok) {
                    const json = await res.json();
                    data = json.args;
                }
            }

            if (data) {
                setRichMetadata(prev => ({
                    ...prev,
                    [name]: {
                        ...(prev[name] || {}),
                        type,
                        fields: data,
                    }
                }));
            }
        } catch (e) {
            console.error(`Failed to fetch metadata for ${name}`, e);
        }
    };

    const toggleItem = (name: string, type: 'table' | 'rpc' | 'edge' | 'auth' | 'storage' | 'realtime' | 'vector') => {
        const next = new Set(expandedItems);
        if (next.has(name)) {
            next.delete(name);
        } else {
            next.add(name);
            if (type === 'table' || type === 'rpc') fetchMetadata(name, type);
            // Default table operation to GET if not set
            if (type === 'table' && !tableOperations[name]) {
                setTableOperations(prev => ({ ...prev, [name]: 'GET' }));
            }
        }
        setExpandedItems(next);
    };

    const toggleParams = (name: string) => {
        const next = new Set(expandedParams);
        if (next.has(name)) next.delete(name);
        else next.add(name);
        setExpandedParams(next);
    };

    const toggleSidebarGroup = (group: string) => {
        if (isLongPress.current) return; // Ignore click if it was a long press

        const next = new Set(expandedSidebarGroups);
        if (next.has(group)) next.delete(group);
        else next.add(group);
        setExpandedSidebarGroups(next);
    };

    // --- DRAG & DROP HANDLERS ---
    const handleDragStart = (e: React.DragEvent, group: string) => {
        e.stopPropagation();
        setDraggedGroup(group);
        e.dataTransfer.effectAllowed = 'move';
    };

    const handleDragOver = (e: React.DragEvent, group: string) => {
        e.preventDefault();
        if (!draggedGroup || draggedGroup === group) return;

        const newOrder = [...groupOrder];
        const fromIndex = newOrder.indexOf(draggedGroup);
        const toIndex = newOrder.indexOf(group);

        // Swap
        newOrder.splice(fromIndex, 1);
        newOrder.splice(toIndex, 0, draggedGroup);

        setGroupOrder(newOrder);
    };

    const handleDragEnd = () => {
        setDraggedGroup(null);
    };

    // --- LONG PRESS HANDLERS ---
    const startLongPress = (group: string, items: any[]) => {
        isLongPress.current = false;
        longPressTimer.current = setTimeout(() => {
            isLongPress.current = true;
            // Select All Logic
            const next = new Set(selectedItems);
            setIsMultiSelectMode(true);

            items.forEach(item => {
                // Item might be a string (table name) or object (endpoint def)
                const id = typeof item === 'string' ? item : item.id;
                next.add(id);
            });
            setSelectedItems(next);
            // Auto-expand group if selecting all
            setExpandedSidebarGroups(prev => new Set(prev).add(group));
        }, 500); // 500ms threshold
    };

    const endLongPress = () => {
        if (longPressTimer.current) {
            clearTimeout(longPressTimer.current);
            longPressTimer.current = null;
        }
    };

    const setTableOperation = (tableName: string, op: string) => {
        setTableOperations(prev => ({ ...prev, [tableName]: op }));
    };

    // --- SELECTION LOGIC ---

    const handleSidebarClick = (name: string) => {
        if (isMultiSelectMode) {
            const next = new Set(selectedItems);
            if (next.has(name)) next.delete(name);
            else next.add(name);
            setSelectedItems(next);
        } else {
            setSelectedItems(new Set([name]));

            if (!expandedItems.has(name)) {
                // Try to guess type based on lists
                let type: 'table' | 'rpc' | 'edge' | 'auth' | 'storage' | 'realtime' | 'vector' = 'table';
                if (apiItems.rpcs.includes(name)) type = 'rpc';
                else if (apiItems.edge.includes(name)) type = 'edge';
                else if (apiItems.auth.some(a => a.id === name)) type = 'auth';
                else if (apiItems.storage.some(s => s.id === name)) type = 'storage';
                else if (apiItems.realtime.some(r => r.id === name)) type = 'realtime';
                else if (apiItems.vector.some(v => v.id === name)) type = 'vector';

                toggleItem(name, type);
            }

            setTimeout(() => {
                document.getElementById(`ref-${name}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
            }, 100);
        }
    };

    const handleSidebarDoubleClick = (name: string) => {
        setIsMultiSelectMode(true);
        const next = new Set(selectedItems);
        next.add(name);
        setSelectedItems(next);
    };

    const clearSelection = () => {
        setSelectedItems(new Set());
        setIsMultiSelectMode(false);
    };

    const getBaseUrl = (style?: 'sovereign' | 'legacy') => {
        const activeStyle = style || routingStyle;
        let host = window.location.origin;
        if (projectData?.custom_domain) {
            host = `https://${projectData.custom_domain}`;
        } else {
            host = `${host}/api/data/${projectId}`;
        }

        // Apply path-based branch routing if we are in a non-live environment branch
        if (activeBranch !== 'live') {
            host = `${host}/branch/${activeBranch}`;
        }

        return activeStyle === 'legacy' ? `${host}/rest/v1` : host;
    };

    const getAuthBaseUrl = () => {
        let host = window.location.origin;
        if (projectData?.custom_domain) {
            host = `https://${projectData.custom_domain}`;
        } else {
            host = `${host}/api/data/${projectId}`;
        }

        // Apply path-based branch routing if we are in a non-live environment branch
        if (activeBranch !== 'live') {
            host = `${host}/branch/${activeBranch}`;
        }

        return host;
    };

    const getEdgeUrl = (fnName: string) => {
        let host = window.location.origin;
        if (projectData?.custom_domain) {
            host = `https://${projectData.custom_domain}`;
        } else {
            host = `${host}/api/data/${projectId}`;
        }

        // Apply path-based branch routing if we are in a non-live environment branch
        if (activeBranch !== 'live') {
            return `${host}/branch/${activeBranch}/edge/${fnName}`;
        }
        return `${host}/edge/${fnName}`;
    };

    const getSwaggerUrl = () => {
        const basePath = specFormat === 'swagger2' ? '/rest/v1' : '';
        let host = window.location.origin;
        if (projectData?.custom_domain) {
            host = `https://${projectData.custom_domain}`;
        } else {
            host = `${host}/api/data/${projectId}`;
        }

        // Apply path-based branch routing if we are in a non-live environment branch
        if (activeBranch !== 'live') {
            return `${host}/branch/${activeBranch}${basePath}`;
        }
        return `${host}${basePath}`;
    };

    const safeCopyToClipboard = (text: string, id: string) => {
        if (navigator.clipboard && window.isSecureContext) {
            navigator.clipboard.writeText(text);
        } else {
            const textArea = document.createElement("textarea");
            textArea.value = text;
            textArea.style.position = "fixed";
            textArea.style.left = "-9999px";
            document.body.appendChild(textArea);
            textArea.focus();
            textArea.select();
            document.execCommand('copy');
            document.body.removeChild(textArea);
        }
        setCopiedUrl(id);
        setTimeout(() => setCopiedUrl(null), 2000);
    };

    const handleDownloadOpenAPI = () => {
        const blob = new Blob([JSON.stringify(spec, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a'); a.href = url; a.download = `${projectId}-openapi.json`; a.click();
    };

    const handleGenerateDoc = async (tableName: string) => {
        setGenerating(true);
        try {
            const res = await fetch(`/api/data/${projectId}/ai/draft-doc`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${localStorage.getItem('cascata_token')}`
                },
                body: JSON.stringify({ tableName })
            });

            if (!res.ok) throw new Error('Generation failed');
            const newDoc = await res.json();

            setGuides(prev => [newDoc, ...prev]);
            setSelectedGuide(newDoc);
            setShowGenModal(false);
            setActiveTab('guides');
        } catch (e) {
            alert("Failed to generate documentation.");
        } finally {
            setGenerating(false);
        }
    };

    // Helper to format values based on curl display mode
    // For ENUM types, uses the type name (e.g., <product_status>) which is more informative than field name (e.g., <status>)
    const formatPlaceholder = (value: string, fieldName: string, fieldType?: string): string => {
        // Determine what name to use in the placeholder
        let placeholderName = fieldName;
        if (fieldType) {
            const normalizedType = fieldType.toLowerCase();
            const typeWithoutSchema = normalizedType.split('.').pop() || normalizedType;
            // If it's an ENUM type (exists in enumTypes), use the type name instead of field name
            if (enumTypes && (enumTypes[typeWithoutSchema] || enumTypes[normalizedType])) {
                placeholderName = typeWithoutSchema; // Use type name like "product_status" instead of "status"
            }
        }
        if (curlFormat === 'angle') return `<${placeholderName}>`;
        if (curlFormat === 'bracket') return `[${placeholderName}]`;
        if (curlFormat === 'double-brace') return `{{${placeholderName}}}`;
        if (curlFormat === 'dollar') return `\${${placeholderName}}`;
        if (curlFormat === 'custom') return customPattern.replace('%s', placeholderName);
        return value; // 'example' mode - return real value
    };

    // Helper to recursively format body object values for static endpoints (auth, storage, vector)
    const formatBodyObject = (obj: any, isPasskey: boolean, currentKey: string = ''): any => {
        if (obj === null || obj === undefined) return obj;
        if (Array.isArray(obj)) {
            return obj.map(item => {
                if (typeof item === 'object' && item !== null) {
                    return formatBodyObject(item, isPasskey, currentKey);
                }
                const placeholderName = currentKey ? (currentKey.endsWith('s') ? currentKey.slice(0, -1) : currentKey) : 'item';
                return formatPlaceholder(String(item), placeholderName);
            });
        }
        if (typeof obj === 'object') {
            const result: any = {};
            for (const [key, value] of Object.entries(obj)) {
                if (value === null || value === undefined) {
                    result[key] = value;
                } else if (typeof value === 'object') {
                    result[key] = formatBodyObject(value, isPasskey, key);
                } else {
                    let placeholderName = key;
                    if (isPasskey) {
                        if (key === 'id') placeholderName = 'id-Passkey';
                        else if (key === 'rawId') placeholderName = 'rawId-Passkey';
                        else if (key === 'type') placeholderName = 'type-key';
                    }
                    if (key === 'clientDataJSON') placeholderName = 'clientDataJSONPasskey';
                    else if (key === 'attestationObject') placeholderName = 'attestationObject-Passkey';
                    else if (key === 'authenticatorData') placeholderName = 'authenticatorDataPasskey';
                    else if (key === 'signature') placeholderName = 'signaturePasskey';
                    else if (key === 'userHandle') placeholderName = 'userHandlePasskey';

                    result[key] = formatPlaceholder(String(value), placeholderName);
                }
            }
            return result;
        }
        return obj;
    };

    const generateCurl = (method: string, path: string, type: 'table' | 'rpc' | 'edge' | 'auth' | 'storage' | 'realtime' | 'vector', endpointDef?: any) => {
        let url = '';
        let bucketName = buckets.length > 0 ? buckets[0].name : 'my-bucket';
        let safePath = path.replace(':bucket', bucketName).replace(':path', 'file.png');

        const anonKey = getActiveAnonKey();
        const displayKey = formatPlaceholder(anonKey, 'apikey');
        // If user pasted a bearer token from clipboard (by re-clicking 'ex.' while already selected), use it
        const authValue = (curlFormat === 'example' && clipboardBearer) ? clipboardBearer : anonKey;
        const displayAuth = formatPlaceholder(authValue, 'USER_ACCESS_TOKEN');

        // REALTIME (SSE) Special Case
        if (type === 'realtime') {
            let rtBase = getAuthBaseUrl();
            url = `${rtBase}/realtime?apikey=${displayKey}&table=users`;
            return `curl -N -H "Accept: text/event-stream" -H "Authorization: Bearer ${displayAuth}" "${url}"`;
        }

        if (type === 'auth') {
            url = `${getAuthBaseUrl()}${endpointDef.path}`;
        } else if (type === 'edge') {
            const fnName = path.replace('/edge/', '');
            url = getEdgeUrl(fnName);
        } else if (type === 'vector') {
            url = `${getAuthBaseUrl()}${endpointDef.path}`;
        } else if (type === 'storage') {
            // Base URL for storage
            let baseUrl = getBaseUrl().replace('/rest/v1', ''); // Strip rest base
            url = `${baseUrl}${safePath}`;
        } else {
            // Handle PostgREST URLs
            let entityName = '';
            if (type === 'table') {
                const match = path.match(/^\/tables\/(.+)\/data$/);
                entityName = match ? match[1] : path;
            } else {
                entityName = path.replace(/^\/rpc\//, '');
            }

            if (entityName && type === 'table') {
                url = `${getBaseUrl()}/${entityName}`;
            } else if (entityName && type === 'rpc') {
                url = `${getBaseUrl()}/rpc/${entityName}`;
            } else {
                url = `${getBaseUrl()}${path}`;
            }
        }


        let cmd = `curl -X ${method} "${url}" \\\n`;
        const replaceUrl = (nextUrl: string) => {
            cmd = cmd.replace(`"${url}"`, `"${nextUrl}"`);
            url = nextUrl;
        };
        const appendQuery = (baseUrl: string, params: Record<string, string>) => {
            const qs = Object.entries(params)
                .filter(([, value]) => value !== undefined && value !== null && value !== '')
                // Only encode the key; values are already formatted placeholders (e.g. <id>, ${id}, {{id}})
                // so encoding them would corrupt the display (e.g. ${id} → %24%7Bid%7D)
                .map(([key, value]) => `${encodeURIComponent(key)}=${value}`)
                .join('&');
            if (!qs) return baseUrl;
            return `${baseUrl}${baseUrl.includes('?') ? '&' : '?'}${qs}`;
        };

        // Headers
        cmd += `  -H "apikey: ${displayKey}"`;

        if (type === 'auth') {
            if (endpointDef.auth_required) {
                const userToken = formatPlaceholder(authValue, 'USER_ACCESS_TOKEN');
                cmd += ` \\\n  -H "Authorization: Bearer ${userToken}"`;
            }
        } else {
            cmd += ` \\\n  -H "Authorization: Bearer ${displayAuth}"`;
        }

        // Special Handling for File Upload
        if (type === 'storage' && endpointDef?.is_upload) {
            cmd += ` \\\n  -F "file=@./image.png"`;
            // Form fields if needed
            if (endpointDef.body && Object.keys(endpointDef.body).length > 0) {
                for (const [key, val] of Object.entries(endpointDef.body)) {
                    const displayVal = formatPlaceholder(String(val), key);
                    cmd += ` \\\n  -F "${key}=${displayVal}"`;
                }
            }
            return cmd; // Return early for upload
        }

        // Content-Type & Body
        if (method === 'POST' || method === 'PATCH' || method === 'PUT') {
            cmd += ` \\\n  -H "Content-Type: application/json"`;

            let bodyPayload = '{}';

            if (type === 'auth' || type === 'storage' || type === 'vector') {
                if (endpointDef?.body && Object.keys(endpointDef.body).length > 0) {
                    let finalBody = { ...endpointDef.body };

                    // Dynamic Auth Strategy Injection
                    if (type === 'auth') {
                        const strategy = dynamicStrategies.find((s: any) => s.id === selectedStrategy) || dynamicStrategies[0];

                        // Handle generic identifier replacement
                        if (strategy) {
                            if (finalBody.email || finalBody.identifier || finalBody.provider) {
                                delete finalBody.email;
                                delete finalBody.cpf;
                                delete finalBody.phone;
                                delete finalBody.identifier;
                                delete finalBody.provider;

                                if (strategy.id === 'email') {
                                    finalBody.email = strategy.identifierValue || 'user@example.com';
                                } else {
                                    finalBody.provider = strategy.provider;
                                    finalBody.identifier = strategy.identifierValue || 'your-identifier';
                                }
                            }

                            // Handle username field for passkey start endpoints
                            if (finalBody.hasOwnProperty('username')) {
                                finalBody.username = strategy.identifierValue || 'user@example.com';
                            }
                        }
                    }

                    const isPasskey = (endpointDef?.path || path || '').includes('passkey');
                    const formattedBody = formatBodyObject(finalBody, isPasskey);

                    // Inject optional step-up code for Sign In / Update User / Recover Password
                    const authStepUpTargets = ['auth_login', 'auth_update_user', 'auth_recover'];
                    if (authStepUpCode && endpointDef?.id && authStepUpTargets.includes(endpointDef.id)) {
                        const codeValue = authStepUpCode === 'passkey_code' ? 'passkey_assertion_code' : stepUpCodeValue;
                        formattedBody[authStepUpCode] = formatPlaceholder(codeValue, authStepUpCode);
                    }

                    bodyPayload = JSON.stringify(formattedBody, null, 2);
                }
            } else if (type === 'edge') {
                const edgeBody: any = {};
                if (curlFormat === 'example') {
                    edgeBody.foo = 'bar';
                } else {
                    edgeBody.payload = formatPlaceholder('json_payload', 'json_payload');
                }
                bodyPayload = JSON.stringify(edgeBody, null, 2);
            } else {
                let entityName = type === 'table' ? path.replace('/tables/', '').replace('/data', '') : path.replace('/rpc/', '');
                const metadata = richMetadata[entityName];
                let body: any = {};
                let hasOtpProtected = false;
                let protectedCols: string[] = [];
                const tableStepUpFactors = type === 'table' ? getTableStepUpFactorsForMethod(metadata, method) : [];

                if (metadata && metadata.fields) {
                    metadata.fields.forEach((field: any) => {
                        if (type === 'table' && (field.lockLevel === 'code_protected' || field.lockLevel === 'otp_protected')) {
                            hasOtpProtected = true;
                            protectedCols.push(`${field.name} (${field.methods || field.Methods || 'TOTP/MFA, Passkey, OTP'})`);
                        }

                        if (type === 'rpc' && (field.mode === 'OUT' || field.mode === 'TABLE')) return;
                        // Check if field is an ENUM type - if so, include it even with default
                        const fieldType = field.type?.toLowerCase() || '';
                        const enumName = fieldType.split('.').pop() || fieldType;
                        const isEnum = enumTypes && (enumTypes[enumName] || enumTypes[fieldType]);
                        // Skip fields with defaults UNLESS they are ENUMs (to show valid values)
                        if (type === 'table' && !isEnum && field.defaultValue !== null && field.defaultValue !== undefined) return;
                        if (type === 'table' && field.isPrimaryKey && field.type.includes('int')) return;
                        // PASS formatPattern and enumTypes to generate realistic examples
                        const realValue = String(generateSmartValue(field.name, field.type, field.formatPattern, enumTypes));
                        body[field.name] = formatPlaceholder(realValue, field.name, field.type);
                    });
                }

                if ((hasOtpProtected && method === 'PATCH') || tableStepUpFactors.length > 0) {
                    // Use the user-selected step-up code instead of auto-calculating
                    const provider = selectedStepUpCode.replace('_code', '');
                    const value = provider === 'passkey' ? 'passkey_assertion_code' : stepUpCodeValue;
                    body[selectedStepUpCode] = formatPlaceholder(value, selectedStepUpCode);
                }

                if (Object.keys(body).length > 0) {
                    bodyPayload = JSON.stringify(body, null, 2);
                }
            }

            if (bodyPayload !== '{}') {
                cmd += ` \\\n  -d '${bodyPayload}'`;
            }
        }

        if (method === 'GET' && type === 'table') {
            replaceUrl(`${url}?select=*&limit=10`);
        }

        if ((method === 'DELETE' || method === 'PATCH') && type === 'table') {
            let entityName = path.replace('/tables/', '').replace('/data', '');
            const metadata = richMetadata[entityName];
            let pkCol = 'id';
            let pkType = 'uuid';
            if (metadata && metadata.fields) {
                const pkField = metadata.fields.find((f: any) => f.isPrimaryKey) ||
                                metadata.fields.find((f: any) => f.is_unique || f.isUnique);
                if (pkField) {
                    pkCol = pkField.name;
                    pkType = pkField.type || 'uuid';
                }
            }
            const pkExampleVal = String(generateSmartValue(pkCol, pkType));
            const pkPlaceholder = formatPlaceholder(pkExampleVal, pkCol, pkType);
            replaceUrl(appendQuery(url, { [pkCol]: `eq.${pkPlaceholder}` }));
        }

        if ((method === 'GET' || method === 'DELETE') && type === 'table') {
            const entityName = path.replace('/tables/', '').replace('/data', '');
            const metadata = richMetadata[entityName];
            const tableStepUpFactors = getTableStepUpFactorsForMethod(metadata, method);
            if (tableStepUpFactors.length > 0) {
                const challenge = getStepUpCodeField(tableStepUpFactors);
                replaceUrl(appendQuery(url, {
                    [challenge.key]: formatPlaceholder(challenge.value, challenge.key),
                }));
            }
        }

        if (method === 'GET' && type === 'storage' && endpointDef?.id === 'storage_list_files') {
            replaceUrl(`${url}?path=folder1`);
        }
        if (method === 'DELETE' && type === 'storage') {
            replaceUrl(`${url}?path=folder1/image.png`);
        }

        return cmd;
    };

    const apiItems = useMemo(() => {
        const tables: string[] = [];
        const edgeFunctions: string[] = [];
        const specRpcs: string[] = [];

        if (spec && spec.paths) {
            Object.keys(spec.paths).forEach(path => {
                // Skip root path and utility paths
                if (path === '/' || path === '/metadata' || path === '/health') return;

                // Tables - New format: direct paths like /tess, /teste
                // Match single-level paths that look like table names (not /rpc/, /edge/, etc.)
                const directTableMatch = path.match(/^\/([a-zA-Z_][a-zA-Z0-9_]*)$/);
                if (directTableMatch) {
                    const tableName = directTableMatch[1];
                    // Exclude reserved names
                    if (!['rpc', 'edge', 'auth', 'storage', 'realtime', 'vector', 'tables', 'functions', 'docs', 'rest', 'api'].includes(tableName)) {
                        if (smartSearch(tableName, searchQuery)) {
                            tables.push(tableName);
                        }
                    }
                }

                // Legacy format: /tables/{name}/data
                const legacyTableMatch = path.match(/^\/tables\/(.+)\/data$/);
                if (legacyTableMatch && smartSearch(legacyTableMatch[1], searchQuery)) {
                    tables.push(legacyTableMatch[1]);
                }

                // Edge Functions - OpenAPI 3.0 format: /edge/{name}
                if (path.startsWith('/edge/')) {
                    const fnName = path.replace('/edge/', '');
                    if (smartSearch(fnName, searchQuery)) {
                        edgeFunctions.push(fnName);
                    }
                }

                // RPCs / Edge Functions - Swagger 2.0 format: /rpc/{name}
                if (path.startsWith('/rpc/')) {
                    const rpcName = path.replace('/rpc/', '');
                    // Check tags to distinguish between RPCs and Edge Functions
                    const pathData = spec.paths[path];
                    const tags = pathData?.post?.tags || pathData?.get?.tags || [];
                    if (tags.includes('Edge Functions')) {
                        if (smartSearch(rpcName, searchQuery)) {
                            edgeFunctions.push(rpcName);
                        }
                    } else {
                        // It's a stored procedure/RPC
                        if (smartSearch(rpcName, searchQuery)) {
                            specRpcs.push(rpcName);
                        }
                    }
                }
            });
        }

        // Combine spec RPCs with custom functions
        const rpcs = new Set<string>(specRpcs);
        customFunctions.forEach(fn => {
            if (!SYSTEM_RPC_PREFIXES.some(prefix => fn.name.startsWith(prefix))) {
                if (smartSearch(fn.name, searchQuery)) {
                    rpcs.add(fn.name);
                }
            }
        });

        const auth = AUTH_ENDPOINTS.filter(e => smartSearch(e, searchQuery));
        const storage = STORAGE_ENDPOINTS.filter(e => smartSearch(e, searchQuery));
        const realtime = REALTIME_ENDPOINTS.filter(e => smartSearch(e, searchQuery));
        const vector = VECTOR_ENDPOINTS.filter(e => smartSearch(e, searchQuery));

        return { tables, rpcs: Array.from(rpcs), edge: edgeFunctions, auth, storage, realtime, vector };
    }, [spec, customFunctions, searchQuery]);

    // VISIBLE ITEMS CALCULATION (FILTER LOGIC)
    const visibleItems = useMemo(() => {
        if (selectedItems.size >= 1 && isMultiSelectMode) {
            return {
                tables: apiItems.tables.filter(t => selectedItems.has(t)),
                rpcs: apiItems.rpcs.filter(r => selectedItems.has(r)),
                edge: apiItems.edge.filter(e => selectedItems.has(e)),
                auth: apiItems.auth.filter(a => selectedItems.has(a.id)),
                storage: apiItems.storage.filter(s => selectedItems.has(s.id)),
                realtime: apiItems.realtime.filter(r => selectedItems.has(r.id)),
                vector: apiItems.vector.filter(v => selectedItems.has(v.id))
            };
        }
        if (selectedItems.size === 1 && !isMultiSelectMode) {
            const selected = Array.from(selectedItems)[0];
            return {
                tables: apiItems.tables.filter(t => t === selected),
                rpcs: apiItems.rpcs.filter(r => r === selected),
                edge: apiItems.edge.filter(e => e === selected),
                auth: apiItems.auth.filter(a => a.id === selected),
                storage: apiItems.storage.filter(s => s.id === selected),
                realtime: apiItems.realtime.filter(r => r.id === selected),
                vector: apiItems.vector.filter(v => v.id === selected)
            };
        }
        return apiItems;
    }, [apiItems, selectedItems, isMultiSelectMode]);

    const supabaseConfigCode = `
import { createClient } from '@supabase/supabase-js'

const supabaseUrl = '${getSwaggerUrl().replace('/rest/v1', '')}'
const supabaseKey = '${getActiveAnonKey()}'

export const supabase = createClient(supabaseUrl, supabaseKey)
  `;

    if (loading) return <div className="p-20 flex justify-center"><Loader2 className="animate-spin text-indigo-600" /></div>;

    // Render Helper for Drag & Drop Sidebar
    const renderSidebarGroup = (groupKey: string) => {
        let title = '';
        let items: any[] = [];
        let emptyMsg = 'No items found';

        switch (groupKey) {
            case 'auth':
                title = 'Authentication';
                items = apiItems.auth;
                break;
            case 'realtime':
                title = 'Realtime Engine (SSE)';
                items = apiItems.realtime;
                break;
            case 'vector':
                title = 'Vector Memory (RAG)';
                items = apiItems.vector;
                break;
            case 'storage':
                title = 'Storage Engine';
                items = apiItems.storage;
                break;
            case 'tables':
                title = 'Tables & Views';
                items = apiItems.tables;
                emptyMsg = 'No tables found';
                break;
            case 'edge':
                title = 'Edge Functions';
                items = apiItems.edge;
                emptyMsg = 'No functions found';
                break;
            case 'rpc':
                title = 'Stored Procedures';
                items = apiItems.rpcs;
                emptyMsg = 'No RPCs found';
                break;
        }

        // Smart Search Indicator Logic
        const hasHiddenMatches = searchQuery && !expandedSidebarGroups.has(groupKey) && items.length > 0;

        // Group Header Selection Icon (Dynamic based on group type)
        const GroupIcon = () => {
            if (groupKey === 'auth') return <Fingerprint size={12} className={selectedItems.has(items[0]?.id) ? 'opacity-100' : 'opacity-50'} />;
            if (groupKey === 'realtime') return <Radio size={12} />;
            if (groupKey === 'vector') return <Share2 size={12} />;
            if (groupKey === 'storage') return <HardDrive size={12} />;
            if (groupKey === 'tables') return <TableIcon size={12} />; // Changed to TableIcon for Tables
            if (groupKey === 'edge') return <Globe size={12} />;
            if (groupKey === 'rpc') return <Zap size={12} />;
            return null;
        }

        return (
            <div
                key={groupKey}
                draggable
                onDragStart={(e) => handleDragStart(e, groupKey)}
                onDragOver={(e) => handleDragOver(e, groupKey)}
                onDragEnd={handleDragEnd}
                className={`border rounded-xl overflow-hidden bg-white transition-all ${draggedGroup === groupKey ? 'opacity-50 border-dashed border-indigo-400' : 'border-slate-100'} ${hasHiddenMatches ? 'ring-2 ring-indigo-300 ring-offset-1' : ''}`}
            >
                <div
                    className={`w-full flex items-center justify-between px-4 py-3 cursor-grab active:cursor-grabbing transition-colors ${expandedSidebarGroups.has(groupKey) ? 'bg-indigo-600 text-white hover:bg-indigo-700' : `${hasHiddenMatches ? 'bg-indigo-50/50' : 'bg-slate-50'} hover:bg-slate-100`}`}
                    onClick={() => toggleSidebarGroup(groupKey)}
                    onMouseDown={() => startLongPress(groupKey, items)}
                    onMouseUp={endLongPress}
                    onMouseLeave={endLongPress}
                    onTouchStart={() => startLongPress(groupKey, items)}
                    onTouchEnd={endLongPress}
                >
                    <div className="flex items-center gap-2">
                        <GripVertical size={14} className={expandedSidebarGroups.has(groupKey) ? 'text-indigo-300' : 'text-slate-300'} />
                        <h4 className={`text-[10px] font-black uppercase tracking-widest select-none ${expandedSidebarGroups.has(groupKey) ? 'text-white' : 'text-slate-500'}`}>{title}</h4>
                    </div>
                    {expandedSidebarGroups.has(groupKey) ? <ChevronDown size={14} className="text-white" /> : <ChevronRight size={14} className="text-slate-400" />}
                </div>

                {expandedSidebarGroups.has(groupKey) && (
                    <div className="space-y-1 p-2 max-h-[350px] overflow-y-auto custom-scrollbar">
                        {items.map(item => {
                            const id = typeof item === 'string' ? item : item.id;
                            const name = typeof item === 'string' ? item : item.name;

                            return (
                                <button
                                    key={id}
                                    onClick={() => handleSidebarClick(id)}
                                    onDoubleClick={() => handleSidebarDoubleClick(id)}
                                    className={`w-full text-left px-3 py-2 rounded-lg text-xs font-bold transition-all flex items-center gap-2 
                                    ${selectedItems.has(id) ? 'bg-indigo-600 text-white shadow-md' : 'text-slate-600 hover:bg-slate-50'}`}
                                >
                                    <div className="flex-1 flex items-center gap-2 truncate">
                                        <GroupIcon />
                                        {name}
                                    </div>
                                    {selectedItems.has(id) && <Check size={12} />}
                                </button>
                            );
                        })}
                        {items.length === 0 && <p className="text-[10px] text-slate-300 px-3 italic py-2">{emptyMsg}</p>}
                    </div>
                )}
            </div>
        );
    };

    return (
        <div className="p-10 max-w-7xl mx-auto w-full space-y-10 pb-40">
            <header className="flex flex-col gap-8">
                <div className="flex flex-col lg:flex-row items-start lg:items-center justify-between gap-6">
                    <div className="flex items-center gap-4">
                        <div className="w-14 h-14 bg-emerald-600 text-white rounded-[1.5rem] flex items-center justify-center shadow-xl">
                            <BookOpen size={28} />
                        </div>
                        <div>
                            <h1 className="text-4xl font-black text-slate-900 tracking-tighter">Documentation</h1>
                            <p className="text-slate-500 font-medium">Auto-generated API Reference & Integration Guides</p>
                        </div>
                    </div>

                    <div className="flex flex-wrap items-center gap-4">

                        {/* APP CLIENT KEY SELECTOR */}
                        <div className="flex items-center gap-2 bg-indigo-50 border border-indigo-100 p-1.5 rounded-2xl shadow-sm">
                            <Key size={14} className="text-indigo-400 ml-2" />
                            <select
                                value={activeKeyId}
                                onChange={(e) => setActiveKeyId(e.target.value)}
                                title="Change the active anon_key used in the code examples"
                                className="bg-transparent border-none text-xs font-bold text-indigo-900 outline-none pr-4 cursor-pointer"
                            >
                                <option value="default">Default Client (Base Key)</option>
                                {projectData?.metadata?.app_clients?.map((client: any) => (
                                    <option key={client.id} value={client.id}>{client.name}</option>
                                ))}
                            </select>
                        </div>

                        <button onClick={handleDownloadOpenAPI} className="px-4 py-2.5 bg-slate-900 text-white rounded-xl text-xs font-black uppercase tracking-widest flex items-center gap-2 hover:bg-indigo-600 transition-all shadow-lg">
                            <FileJson size={16} /> OpenAPI (JSON)
                        </button>

                        <div className="flex bg-slate-100 p-1.5 rounded-2xl">
                            <button onClick={() => setRoutingStyle('sovereign')} className={`px-4 py-2.5 rounded-xl text-[10px] font-black uppercase tracking-widest transition-all ${routingStyle === 'sovereign' ? 'bg-indigo-600 text-white shadow-md' : 'text-slate-400 hover:text-slate-600'}`}>Sovereign (Modern)</button>
                            <button onClick={() => setRoutingStyle('legacy')} className={`px-4 py-2.5 rounded-xl text-[10px] font-black uppercase tracking-widest transition-all ${routingStyle === 'legacy' ? 'bg-slate-700 text-white shadow-md' : 'text-slate-400 hover:text-slate-600'}`}>Legacy (Supabase)</button>
                        </div>

                        <div className="flex bg-slate-100 p-1.5 rounded-2xl">
                            <button onClick={() => setSpecFormat('openapi3')} className={`px-4 py-2.5 rounded-xl text-[10px] font-black uppercase tracking-widest transition-all ${specFormat === 'openapi3' ? 'bg-emerald-600 text-white shadow-md' : 'text-slate-400 hover:text-slate-600'}`}>OpenAPI 3.0</button>
                            <button onClick={() => setSpecFormat('swagger2')} className={`px-4 py-2.5 rounded-xl text-[10px] font-black uppercase tracking-widest transition-all ${specFormat === 'swagger2' ? 'bg-orange-600 text-white shadow-md' : 'text-slate-400 hover:text-slate-600'}`}>Swagger 2.0</button>
                        </div>

                        <div className="w-px h-8 bg-slate-200 mx-2 hidden lg:block" />

                        {/* CURL FORMAT TOGGLE */}
                        <div className="flex items-center gap-2 bg-slate-50 border border-slate-200 p-1.5 rounded-2xl shadow-sm flex-wrap">
                            <span className="text-[10px] font-bold text-slate-400 ml-2 uppercase tracking-wider">Curl:</span>
                            <button
                                onClick={() => setCurlFormat('angle')}
                                title="Format: <variable>"
                                className={`px-2 py-1.5 rounded-lg text-xs font-black transition-all ${curlFormat === 'angle' ? 'bg-indigo-600 text-white shadow-md' : 'text-slate-500 hover:text-slate-700 hover:bg-slate-100'}`}
                            >&lt;&gt;</button>
                            <button
                                onClick={() => setCurlFormat('bracket')}
                                title="Format: [variable]"
                                className={`px-2 py-1.5 rounded-lg text-xs font-black transition-all ${curlFormat === 'bracket' ? 'bg-indigo-600 text-white shadow-md' : 'text-slate-500 hover:text-slate-700 hover:bg-slate-100'}`}
                            >[]</button>
                            <button
                                onClick={() => setCurlFormat('double-brace')}
                                title="Format: {{variable}}"
                                className={`px-2 py-1.5 rounded-lg text-xs font-black transition-all ${curlFormat === 'double-brace' ? 'bg-indigo-600 text-white shadow-md' : 'text-slate-500 hover:text-slate-700 hover:bg-slate-100'}`}
                            >{'{{}}'}</button>
                            <button
                                onClick={() => setCurlFormat('dollar')}
                                title="Format: ${variable}"
                                className={`px-2 py-1.5 rounded-lg text-xs font-black transition-all ${curlFormat === 'dollar' ? 'bg-indigo-600 text-white shadow-md' : 'text-slate-500 hover:text-slate-700 hover:bg-slate-100'}`}
                            >${ }</button>
                            <button
                                onClick={() => setCurlFormat('custom')}
                                title="Custom format"
                                className={`px-2 py-1.5 rounded-lg text-xs font-black transition-all ${curlFormat === 'custom' ? 'bg-amber-500 text-white shadow-md' : 'text-slate-500 hover:text-slate-700 hover:bg-slate-100'}`}
                            >#</button>
                            <button
                                onClick={handleExampleFormatClick}
                                title="Format: example values (click again to paste Bearer from clipboard)"
                                className={`px-2 py-1.5 rounded-lg text-xs font-black transition-all ${curlFormat === 'example' ? 'bg-emerald-600 text-white shadow-md' : 'text-slate-500 hover:text-slate-700 hover:bg-slate-100'} ${clipboardBearer ? 'ring-2 ring-green-400 ring-offset-1' : ''}`}
                            >ex.</button>

                            {/* Custom Pattern Input - only show when custom is selected */}
                            {curlFormat === 'custom' && (
                                <input
                                    type="text"
                                    value={customPattern}
                                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => setCustomPattern(e.target.value)}
                                    placeholder="Pattern: %s"
                                    title="Use %s where the variable name should appear"
                                    className="w-24 px-2 py-1 text-[10px] font-mono bg-white border border-amber-200 rounded-lg outline-none focus:ring-2 focus:ring-amber-400/30"
                                />
                            )}
                        </div>
                        <div className="w-px h-8 bg-slate-200 mx-2 hidden lg:block" />

                        {/* Active Environment Connection Card */}
                        <div className="flex items-center gap-2">
                            <div className={`px-4 py-2 rounded-xl border flex items-center gap-4 bg-slate-50 border-slate-100 hover:bg-slate-100/50 transition-all shadow-sm`}>
                                <div className="flex items-center gap-2">
                                    <div className={`w-2 h-2 rounded-full animate-pulse ${activeBranch === 'live' ? 'bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]' : 'bg-indigo-500 shadow-[0_0_8px_rgba(99,102,241,0.5)]'}`}></div>
                                    <div className="text-left">
                                        <span className={`text-[8px] font-black uppercase tracking-widest block leading-none ${activeBranch === 'live' ? 'text-emerald-700' : 'text-indigo-700'}`}>
                                            {activeBranch === 'live' ? 'Production (Live)' : 'Active Sandbox Branch'}
                                        </span>
                                        <span className="text-[10px] font-black text-slate-800 tracking-tight mt-0.5 block truncate max-w-[120px]">
                                            {activeBranch}
                                        </span>
                                    </div>
                                </div>
                                <div className="w-px h-6 bg-slate-200" />
                                <div className="flex items-center gap-1.5">
                                    <code className="text-[9px] font-mono text-slate-500 bg-white px-2 py-0.5 rounded-lg border border-slate-200 max-w-[200px] truncate block">
                                        {getBaseUrl()}
                                    </code>
                                    <button 
                                        onClick={() => {
                                            safeCopyToClipboard(getBaseUrl(), 'base-url');
                                            alert('URL Copied to Clipboard!');
                                        }} 
                                        className="p-1.5 bg-white hover:bg-slate-200 border border-slate-200 rounded-lg text-slate-400 hover:text-indigo-600 shadow-sm active:scale-95 transition-all animate-none"
                                        title="Copy Base URL"
                                    >
                                        <Copy size={10} />
                                    </button>
                                </div>
                            </div>
                        </div>

                        <div className="w-px h-8 bg-slate-200 mx-2 hidden lg:block" />

                        <div className="flex bg-slate-100 p-1.5 rounded-2xl">
                            <button onClick={() => setActiveTab('reference')} className={`px-6 py-2.5 rounded-xl text-xs font-black uppercase tracking-widest transition-all ${activeTab === 'reference' ? 'bg-white shadow-md text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}>API Reference</button>
                            <button onClick={() => setActiveTab('connect')} className={`px-6 py-2.5 rounded-xl text-xs font-black uppercase tracking-widest transition-all ${activeTab === 'connect' ? 'bg-white shadow-md text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}>Connect & SDKs</button>
                            <button onClick={() => setActiveTab('guides')} className={`px-6 py-2.5 rounded-xl text-xs font-black uppercase tracking-widest transition-all ${activeTab === 'guides' ? 'bg-white shadow-md text-indigo-600' : 'text-slate-400 hover:text-slate-600'}`}>Guides</button>
                        </div>
                    </div>
                </div>
            </header>

            {/* REFERENCE TAB */}
            {activeTab === 'reference' && (
                <div className="grid grid-cols-1 lg:grid-cols-4 gap-10 min-h-[600px]">
                    {/* Sidebar Navigation */}
                    <aside className="space-y-6 lg:sticky lg:top-8 self-start">
                        <div className="relative mb-6">
                            <Search className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-400" size={16} />
                            <input
                                value={searchQuery}
                                onChange={(e) => setSearchQuery(e.target.value)}
                                placeholder="Search entities..."
                                className="w-full pl-10 pr-4 py-3 bg-white border border-slate-200 rounded-xl text-xs font-bold outline-none focus:ring-2 focus:ring-indigo-500/20"
                            />
                        </div>

                        {(selectedItems.size > 0) && (
                            <div className="bg-indigo-50 border border-indigo-100 p-3 rounded-xl flex justify-between items-center animate-in slide-in-from-left-2 mb-6">
                                <span className="text-[10px] font-black text-indigo-700 uppercase tracking-widest">{selectedItems.size} Selected</span>
                                <button onClick={clearSelection} className="text-[10px] font-bold text-slate-400 hover:text-rose-600">Clear</button>
                            </div>
                        )}

                        <div className="space-y-2">
                            {groupOrder.map(groupKey => renderSidebarGroup(groupKey))}
                        </div>
                    </aside>

                    {/* Main Content */}
                    <div className="lg:col-span-3 space-y-12">
                        {groupOrder.map(groupKey => {
                            // Render content blocks based on group order
                            switch (groupKey) {
                                case 'realtime':
                                    if (visibleItems.realtime.length === 0) return null;
                                    return (
                                        <div key="realtime" className="space-y-8 animate-in fade-in slide-in-from-bottom-2">
                                            {visibleItems.realtime.map(endpoint => {
                                                const isExpanded = expandedItems.has(endpoint.id);
                                                return (
                                                    <div key={endpoint.id} id={`ref-${endpoint.id}`} className={`bg-white border transition-all rounded-[2rem] overflow-hidden ${isExpanded ? 'border-amber-200 shadow-xl' : 'border-slate-200 hover:border-amber-200'}`}>
                                                        <div
                                                            onClick={() => toggleItem(endpoint.id, 'realtime')}
                                                            className="p-6 flex items-center justify-between cursor-pointer bg-white hover:bg-slate-50/50 transition-colors"
                                                        >
                                                            <div className="flex items-center gap-4">
                                                                <div className="w-10 h-10 bg-amber-500 text-white rounded-xl flex items-center justify-center">
                                                                    <Radio size={20} />
                                                                </div>
                                                                <div>
                                                                    <h3 className="text-lg font-black text-slate-900 tracking-tight">{endpoint.name}</h3>
                                                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">{endpoint.description}</p>
                                                                </div>
                                                            </div>
                                                            <div className="flex items-center gap-3">
                                                                <span className="px-2 py-1 rounded-md text-[9px] font-black uppercase bg-slate-100 text-slate-600">STREAM</span>
                                                                {isExpanded ? <ChevronDown size={20} className="text-slate-300" /> : <ChevronRight size={20} className="text-slate-300" />}
                                                            </div>
                                                        </div>
                                                        {isExpanded && (
                                                            <div className="border-t border-slate-100 p-6 bg-slate-50/50">
                                                                <CodeBlock
                                                                    label="Connect Command (SSE)"
                                                                    code={generateCurl(endpoint.method, endpoint.path, 'realtime', endpoint)}
                                                                    onCopy={() => safeCopyToClipboard(generateCurl(endpoint.method, endpoint.path, 'realtime', endpoint), endpoint.id)}
                                                                    copied={copiedUrl === endpoint.id}
                                                                />
                                                                <p className="text-[10px] text-slate-400 mt-2 px-1">Use <code>EventSource</code> in JS clients to consume this stream.</p>
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    );

                                case 'vector':
                                    if (visibleItems.vector.length === 0) return null;
                                    return (
                                        <div key="vector" className="space-y-8 animate-in fade-in slide-in-from-bottom-2">
                                            {visibleItems.vector.map(endpoint => {
                                                const isExpanded = expandedItems.has(endpoint.id);
                                                return (
                                                    <div key={endpoint.id} id={`ref-${endpoint.id}`} className={`bg-white border transition-all rounded-[2rem] overflow-hidden ${isExpanded ? 'border-purple-200 shadow-xl' : 'border-slate-200 hover:border-purple-200'}`}>
                                                        <div
                                                            onClick={() => toggleItem(endpoint.id, 'vector')}
                                                            className="p-6 flex items-center justify-between cursor-pointer bg-white hover:bg-slate-50/50 transition-colors"
                                                        >
                                                            <div className="flex items-center gap-4">
                                                                <div className="w-10 h-10 bg-purple-600 text-white rounded-xl flex items-center justify-center">
                                                                    <Share2 size={20} />
                                                                </div>
                                                                <div>
                                                                    <h3 className="text-lg font-black text-slate-900 tracking-tight">{endpoint.name}</h3>
                                                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">{endpoint.description}</p>
                                                                </div>
                                                            </div>
                                                            <div className="flex items-center gap-3">
                                                                <span className="px-2 py-1 rounded-md text-[9px] font-black uppercase bg-purple-50 text-purple-700">{endpoint.method}</span>
                                                                {isExpanded ? <ChevronDown size={20} className="text-slate-300" /> : <ChevronRight size={20} className="text-slate-300" />}
                                                            </div>
                                                        </div>
                                                        {isExpanded && (
                                                            <div className="border-t border-slate-100 p-6 bg-slate-50/50">
                                                                <CodeBlock
                                                                    label="Vector Operation"
                                                                    code={generateCurl(endpoint.method, endpoint.path, 'vector', endpoint)}
                                                                    onCopy={() => safeCopyToClipboard(generateCurl(endpoint.method, endpoint.path, 'vector', endpoint), endpoint.id)}
                                                                    copied={copiedUrl === endpoint.id}
                                                                />
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    );

                                case 'auth':
                                    if (visibleItems.auth.length === 0) return null;
                                    return (
                                        <div key="auth" className="space-y-8 animate-in fade-in slide-in-from-bottom-2">
                                            {visibleItems.auth.map(endpoint => {
                                                const isExpanded = expandedItems.has(endpoint.id);
                                                return (
                                                    <div key={endpoint.id} id={`ref-${endpoint.id}`} className={`bg-white border transition-all rounded-[2rem] overflow-hidden ${isExpanded ? 'border-indigo-200 shadow-xl' : 'border-slate-200 hover:border-indigo-200'}`}>
                                                        <div
                                                            onClick={() => toggleItem(endpoint.id, 'auth')}
                                                            className="p-6 flex items-center justify-between cursor-pointer bg-white hover:bg-slate-50/50 transition-colors"
                                                        >
                                                            <div className="flex items-center gap-4">
                                                                <div className="w-10 h-10 bg-slate-900 text-white rounded-xl flex items-center justify-center">
                                                                    <Users size={20} />
                                                                </div>
                                                                <div>
                                                                    <h3 className="text-lg font-black text-slate-900 tracking-tight">{endpoint.name}</h3>
                                                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">{endpoint.description}</p>
                                                                </div>
                                                            </div>
                                                            <div className="flex items-center gap-3">
                                                                <span className={`px-2 py-1 rounded-md text-[9px] font-black uppercase ${endpoint.method === 'GET' ? 'bg-blue-50 text-blue-600' : 'bg-emerald-50 text-emerald-600'}`}>{endpoint.method}</span>
                                                                {isExpanded ? <ChevronDown size={20} className="text-slate-300" /> : <ChevronRight size={20} className="text-slate-300" />}
                                                            </div>
                                                        </div>
                                                        {isExpanded && (
                                                            <div className="border-t border-slate-100 p-6 bg-slate-50/50">
                                                                <div className="flex items-center justify-between mb-4 bg-white p-3 rounded-2xl border border-slate-100 shadow-sm">
                                                                    <div className="flex items-center gap-2">
                                                                        <Layers size={14} className="text-indigo-600" />
                                                                        <span className="text-[10px] font-black text-slate-400 uppercase tracking-widest">Select Login Strategy</span>
                                                                    </div>
                                                                    <div className="flex gap-1.5 p-1 bg-slate-50 rounded-xl border border-slate-100">
                                                                        {dynamicStrategies.map((s: any) => {
                                                                            const Icon = s.icon;
                                                                            return (
                                                                                <button
                                                                                    key={s.id}
                                                                                    onClick={() => setSelectedStrategy(s.id)}
                                                                                    className={`px-3 py-2 rounded-lg text-[10px] font-black transition-all flex items-center gap-2 ${selectedStrategy === s.id ? 'bg-white text-indigo-600 shadow-md ring-1 ring-slate-200' : 'text-slate-400 hover:text-slate-600 hover:bg-white/50'}`}
                                                                                >
                                                                                    <Icon size={12} />
                                                                                    {s.name}
                                                                                </button>
                                                                            );
                                                                        })}
                                                                    </div>
                                                                </div>

                                                                <CodeBlock
                                                                    label="Execute Request"
                                                                    code={generateCurl(endpoint.method, endpoint.path, 'auth', endpoint)}
                                                                    onCopy={() => safeCopyToClipboard(generateCurl(endpoint.method, endpoint.path, 'auth', endpoint), endpoint.id)}
                                                                    copied={copiedUrl === endpoint.id}
                                                                />
                                                                {['auth_login', 'auth_update_user', 'auth_recover'].includes(endpoint.id) && (
                                                                    <div className="mt-3 text-xs font-medium text-slate-500">
                                                                        <span className="font-bold text-slate-600">Atenção:</span>{' '}
                                                                        Editar as colunas: telefone (TOTP/MFA) requer validação extra. Envie{' '}
                                                                        {(['totp_code', 'otp_code', 'passkey_code'] as const).map((code, i, arr) => (
                                                                            <span key={code}>
                                                                                <span
                                                                                    className={`font-mono font-bold cursor-pointer hover:underline transition-colors ${authStepUpCode === code ? 'text-indigo-600' : 'text-slate-700 hover:text-indigo-500'}`}
                                                                                    onClick={() => handleAuthStepUpClick(code)}
                                                                                >{code}</span>
                                                                                {i < arr.length - 2 ? ', ' : i === arr.length - 2 ? ' ou ' : '.'}
                                                                            </span>
                                                                        ))}
                                                                    </div>
                                                                )}
                                                                {endpoint.auth_required && (
                                                                    <div className="mt-4 flex items-center gap-2 text-xs font-bold text-amber-600 bg-amber-50 p-2 rounded-lg inline-block">
                                                                        <Lock size={12} /> Requires User Access Token
                                                                    </div>
                                                                )}
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    );

                                case 'storage':
                                    if (visibleItems.storage.length === 0) return null;
                                    return (
                                        <div key="storage" className="space-y-8 animate-in fade-in slide-in-from-bottom-2">
                                            {visibleItems.storage.map(endpoint => {
                                                const isExpanded = expandedItems.has(endpoint.id);
                                                return (
                                                    <div key={endpoint.id} id={`ref-${endpoint.id}`} className={`bg-white border transition-all rounded-[2rem] overflow-hidden ${isExpanded ? 'border-indigo-200 shadow-xl' : 'border-slate-200 hover:border-indigo-200'}`}>
                                                        <div
                                                            onClick={() => toggleItem(endpoint.id, 'storage')}
                                                            className="p-6 flex items-center justify-between cursor-pointer bg-white hover:bg-slate-50/50 transition-colors"
                                                        >
                                                            <div className="flex items-center gap-4">
                                                                <div className="w-10 h-10 bg-indigo-50 text-indigo-600 rounded-xl flex items-center justify-center">
                                                                    <Cloud size={20} />
                                                                </div>
                                                                <div>
                                                                    <h3 className="text-lg font-black text-slate-900 tracking-tight">{endpoint.name}</h3>
                                                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">{endpoint.description}</p>
                                                                </div>
                                                            </div>
                                                            <div className="flex items-center gap-3">
                                                                <span className={`px-2 py-1 rounded-md text-[9px] font-black uppercase ${endpoint.method === 'GET' ? 'bg-blue-50 text-blue-600' : endpoint.method === 'POST' ? 'bg-emerald-50 text-emerald-600' : 'bg-rose-50 text-rose-600'}`}>{endpoint.method}</span>
                                                                {isExpanded ? <ChevronDown size={20} className="text-slate-300" /> : <ChevronRight size={20} className="text-slate-300" />}
                                                            </div>
                                                        </div>
                                                        {isExpanded && (
                                                            <div className="border-t border-slate-100 p-6 bg-slate-50/50">
                                                                <CodeBlock
                                                                    label="Execute Request"
                                                                    code={generateCurl(endpoint.method, endpoint.path, 'storage', endpoint)}
                                                                    onCopy={() => safeCopyToClipboard(generateCurl(endpoint.method, endpoint.path, 'storage', endpoint), endpoint.id)}
                                                                    copied={copiedUrl === endpoint.id}
                                                                />
                                                                {endpoint.is_upload && (
                                                                    <p className="text-[10px] text-slate-400 mt-2 px-1 flex items-center gap-1"><UploadIcon size={10} /> Use Multipart/Form-Data for file uploads.</p>
                                                                )}
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    );

                                case 'tables':
                                    if (visibleItems.tables.length === 0) return null;
                                    return (
                                        <div key="tables" className="space-y-8 animate-in fade-in slide-in-from-bottom-2">
                                            {visibleItems.tables.map(name => {
                                                const path = `/tables/${name}/data`;
                                                const isExpanded = expandedItems.has(name);
                                                const isParamsExpanded = expandedParams.has(name);
                                                const activeOp = tableOperations[name] || 'GET';

                                                return (
                                                    <div key={name} id={`ref-${name}`} className={`bg-white border transition-all rounded-[2rem] overflow-hidden ${isExpanded ? 'border-indigo-200 shadow-xl' : 'border-slate-200 hover:border-indigo-200'}`}>
                                                        <div
                                                            onClick={() => toggleItem(name, 'table')}
                                                            className="p-6 flex items-center justify-between cursor-pointer bg-white hover:bg-slate-50/50 transition-colors"
                                                        >
                                                            <div className="flex items-center gap-4">
                                                                <div
                                                                    className="w-10 h-10 bg-indigo-50 text-indigo-600 rounded-xl flex items-center justify-center cursor-pointer hover:bg-indigo-600 hover:text-white transition-all group/play"
                                                                    onClick={(e) => {
                                                                        e.stopPropagation();
                                                                        const cardKey = `${name}-${activeOp}`;
                                                                        const curl = curlOverrides[cardKey] || generateCurl(activeOp, path, 'table');
                                                                        executeCurl(curl);
                                                                    }}
                                                                    title="Executar request"
                                                                >
                                                                    <Database size={20} className="group-hover/play:hidden" />
                                                                    <Play size={18} className="hidden group-hover/play:block fill-current" />
                                                                </div>
                                                                <div>
                                                                    <h3 className="text-lg font-black text-slate-900 tracking-tight">{name}</h3>
                                                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">REST Resource</p>
                                                                </div>
                                                            </div>
                                                            <div className="flex items-center gap-3">
                                                                {/* Interactive Operation Badges */}
                                                                <div className="flex gap-1" onClick={(e) => e.stopPropagation()}>
                                                                    <button
                                                                        onClick={() => setTableOperation(name, 'GET')}
                                                                        className={`px-2 py-1 rounded-md text-[9px] font-black uppercase transition-all ${activeOp === 'GET' ? 'bg-blue-600 text-white shadow-md' : 'bg-blue-50 text-blue-600 hover:bg-blue-100'}`}
                                                                    >
                                                                        GET
                                                                    </button>
                                                                    <button
                                                                        onClick={() => setTableOperation(name, 'POST')}
                                                                        className={`px-2 py-1 rounded-md text-[9px] font-black uppercase transition-all ${activeOp === 'POST' ? 'bg-emerald-600 text-white shadow-md' : 'bg-emerald-50 text-emerald-600 hover:bg-emerald-100'}`}
                                                                    >
                                                                        POST
                                                                    </button>
                                                                    <button
                                                                        onClick={() => setTableOperation(name, 'PATCH')}
                                                                        className={`px-2 py-1 rounded-md text-[9px] font-black uppercase transition-all ${activeOp === 'PATCH' ? 'bg-orange-600 text-white shadow-md' : 'bg-orange-50 text-orange-600 hover:bg-orange-100'}`}
                                                                    >
                                                                        PATCH
                                                                    </button>
                                                                    <button
                                                                        onClick={() => setTableOperation(name, 'DELETE')}
                                                                        className={`px-2 py-1 rounded-md text-[9px] font-black uppercase transition-all ${activeOp === 'DELETE' ? 'bg-rose-600 text-white shadow-md' : 'bg-rose-50 text-rose-600 hover:bg-rose-100'}`}
                                                                    >
                                                                        DEL
                                                                    </button>
                                                                </div>
                                                                {isExpanded ? <ChevronDown size={20} className="text-slate-300" /> : <ChevronRight size={20} className="text-slate-300" />}
                                                            </div>
                                                        </div>

                                                        {isExpanded && (
                                                            <div className="border-t border-slate-100">
                                                                <CrudExample
                                                                    name={name}
                                                                    path={path}
                                                                    generateCurl={generateCurl}
                                                                    safeCopyToClipboard={safeCopyToClipboard}
                                                                    copiedUrl={copiedUrl}
                                                                    richData={richMetadata[name]}
                                                                    isParamsExpanded={isParamsExpanded}
                                                                    onToggleParams={() => toggleParams(name)}
                                                                    activeOp={activeOp}
                                                                    selectedStepUpCode={selectedStepUpCode}
                                                                    setSelectedStepUpCode={setSelectedStepUpCode}
                                                                    handleStepUpCodeClick={handleStepUpCodeClick}
                                                                    onCurlEdit={(op, val) => setCurlOverrides(prev => ({ ...prev, [`${name}-${op}`]: val }))}
                                                                />
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    );

                                case 'edge':
                                    if (visibleItems.edge.length === 0) return null;
                                    return (
                                        <div key="edge" className="space-y-8 animate-in fade-in slide-in-from-bottom-2">
                                            {visibleItems.edge.map(name => {
                                                const isExpanded = expandedItems.has(name);
                                                const path = `/edge/${name}`;

                                                return (
                                                    <div key={name} id={`ref-${name}`} className={`bg-white border transition-all rounded-[2rem] overflow-hidden ${isExpanded ? 'border-emerald-200 shadow-xl' : 'border-slate-200 hover:border-emerald-200'}`}>
                                                        <div
                                                            onClick={() => toggleItem(name, 'edge')}
                                                            className="p-6 flex items-center justify-between cursor-pointer bg-white hover:bg-slate-50/50 transition-colors"
                                                        >
                                                            <div className="flex items-center gap-4">
                                                                <div className="w-10 h-10 bg-emerald-50 text-emerald-600 rounded-xl flex items-center justify-center">
                                                                    <Globe size={20} />
                                                                </div>
                                                                <div>
                                                                    <h3 className="text-lg font-black text-slate-900 tracking-tight">{name}</h3>
                                                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">Serverless Function</p>
                                                                </div>
                                                            </div>
                                                            <div className="flex items-center gap-3">
                                                                <span className="px-2 py-1 rounded-md bg-emerald-50 text-emerald-600 text-[9px] font-black uppercase">POST</span>
                                                                {isExpanded ? <ChevronDown size={20} className="text-slate-300" /> : <ChevronRight size={20} className="text-slate-300" />}
                                                            </div>
                                                        </div>

                                                        {isExpanded && (
                                                            <div className="border-t border-slate-100 p-6 bg-slate-50/50">
                                                                <CodeBlock
                                                                    label="Invoke Function"
                                                                    code={generateCurl('POST', path, 'edge')}
                                                                    onCopy={() => safeCopyToClipboard(generateCurl('POST', path, 'edge'), path)}
                                                                    copied={copiedUrl === path}
                                                                />
                                                                <p className="text-[10px] text-slate-400 mt-2 px-1">Edge functions run in an isolated V8 environment and accept JSON payload.</p>
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    );

                                case 'rpc':
                                    if (visibleItems.rpcs.length === 0) return null;
                                    return (
                                        <div key="rpc" className="space-y-8 animate-in fade-in slide-in-from-bottom-2">
                                            {visibleItems.rpcs.map(name => {
                                                const isExpanded = expandedItems.has(name);
                                                const isParamsExpanded = expandedParams.has(name);
                                                const path = `/rpc/${name}`;
                                                const meta = richMetadata[name];
                                                const args = meta?.fields || [];

                                                return (
                                                    <div key={name} id={`ref-${name}`} className={`bg-white border transition-all rounded-[2rem] overflow-hidden ${isExpanded ? 'border-amber-200 shadow-xl' : 'border-slate-200 hover:border-amber-200'}`}>
                                                        <div
                                                            onClick={() => toggleItem(name, 'rpc')}
                                                            className="p-6 flex items-center justify-between cursor-pointer bg-white hover:bg-slate-50/50 transition-colors"
                                                        >
                                                            <div className="flex items-center gap-4">
                                                                <div className="w-10 h-10 bg-amber-50 text-amber-600 rounded-xl flex items-center justify-center">
                                                                    <Zap size={20} />
                                                                </div>
                                                                <div>
                                                                    <h3 className="text-lg font-black text-slate-900 tracking-tight">{name}</h3>
                                                                    <p className="text-[10px] text-slate-400 font-bold uppercase tracking-widest mt-0.5">Stored Procedure</p>
                                                                </div>
                                                            </div>
                                                            <div className="flex items-center gap-3">
                                                                <span className="px-2 py-1 rounded-md bg-emerald-50 text-emerald-600 text-[9px] font-black uppercase">POST</span>
                                                                {isExpanded ? <ChevronDown size={20} className="text-slate-300" /> : <ChevronRight size={20} className="text-slate-300" />}
                                                            </div>
                                                        </div>

                                                        {isExpanded && (
                                                            <div className="border-t border-slate-100 p-6 bg-slate-50/50">
                                                                {/* Params Table (Collapsible) */}
                                                                {args.length > 0 && (
                                                                    <div className="mb-6 bg-white rounded-xl border border-slate-200 overflow-hidden">
                                                                        <div
                                                                            onClick={() => toggleParams(name)}
                                                                            className="px-4 py-2 bg-slate-100 border-b border-slate-200 flex justify-between items-center cursor-pointer hover:bg-slate-200/50 transition-colors"
                                                                        >
                                                                            <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest flex items-center gap-2"><ListFilter size={12} /> Arguments</span>
                                                                            {isParamsExpanded ? <ChevronDown size={14} className="text-slate-400" /> : <ChevronRight size={14} className="text-slate-400" />}
                                                                        </div>
                                                                        {isParamsExpanded && (
                                                                            <div className="max-h-60 overflow-y-auto">
                                                                                <table className="w-full text-left animate-in slide-in-from-top-1">
                                                                                    <tbody>
                                                                                        {args.map((arg: any, idx: number) => (
                                                                                            <tr key={idx} className="border-b border-slate-100 last:border-0">
                                                                                                <td className="px-4 py-2 text-xs font-bold font-mono text-indigo-700">{arg.name}</td>
                                                                                                <td className="px-4 py-2 text-xs font-mono text-slate-500">{arg.type}</td>
                                                                                                <td className="px-4 py-2 text-xs font-black uppercase text-slate-400">{arg.mode || 'IN'}</td>
                                                                                            </tr>
                                                                                        ))}
                                                                                    </tbody>
                                                                                </table>
                                                                            </div>
                                                                        )}
                                                                    </div>
                                                                )}

                                                                <CodeBlock
                                                                    label="Execute Function"
                                                                    code={generateCurl('POST', path, 'rpc')}
                                                                    onCopy={() => safeCopyToClipboard(generateCurl('POST', path, 'rpc'), path)}
                                                                    copied={copiedUrl === path}
                                                                />
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    );

                                default:
                                    return null;
                            }
                        })}
                    </div>
                </div>
            )}

            {/* CONNECT TAB (Combined Libraries & Integrations) */}
            {activeTab === 'connect' && (
                <div className="space-y-12 animate-in slide-in-from-right-4">

                    {/* SDKs Section */}
                    <div>
                        <h3 className="text-xl font-black text-slate-900 mb-6 flex items-center gap-2"><Code2 size={24} className="text-indigo-600" /> Client Libraries</h3>
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
                            {/* Supabase JS */}
                            <div className="bg-white border border-emerald-100 rounded-[2.5rem] p-8 shadow-lg relative overflow-hidden group">
                                <div className="flex items-center gap-4 mb-4">
                                    <div className="w-12 h-12 bg-emerald-500 text-white rounded-xl flex items-center justify-center shadow-lg"><Package size={24} /></div>
                                    <div>
                                        <h4 className="text-lg font-black text-slate-900">Supabase JS</h4>
                                        <span className="text-[9px] font-bold bg-emerald-50 text-emerald-600 px-2 py-0.5 rounded uppercase">Recommended</span>
                                    </div>
                                </div>
                                <p className="text-xs text-slate-500 font-medium mb-6">Fully compatible. Use the official client to interact with Cascata.</p>
                                <div className="bg-slate-900 rounded-2xl p-4 border border-slate-800 relative group/code">
                                    <pre className="font-mono text-[10px] text-emerald-400 whitespace-pre-wrap">{supabaseConfigCode.trim()}</pre>
                                    <button onClick={() => safeCopyToClipboard(supabaseConfigCode.trim(), 'supacode')} className="absolute top-2 right-2 p-1.5 bg-white/10 hover:bg-white/20 rounded-lg text-white transition-all opacity-0 group-hover/code:opacity-100">
                                        {copiedUrl === 'supacode' ? <Check size={12} /> : <Copy size={12} />}
                                    </button>
                                </div>
                            </div>

                            {/* Native Fetch */}
                            <div className="bg-white border border-slate-200 rounded-[2.5rem] p-8 shadow-sm hover:shadow-md transition-all">
                                <div className="flex items-center gap-4 mb-4">
                                    <div className="w-12 h-12 bg-slate-900 text-white rounded-xl flex items-center justify-center shadow-lg"><Terminal size={24} /></div>
                                    <div>
                                        <h4 className="text-lg font-black text-slate-900">Native Fetch</h4>
                                        <span className="text-[9px] font-bold bg-slate-100 text-slate-500 px-2 py-0.5 rounded uppercase">Lightweight</span>
                                    </div>
                                </div>
                                <p className="text-xs text-slate-500 font-medium mb-6">Zero dependencies. Use standard HTTP requests.</p>
                                <pre className="bg-slate-50 p-4 rounded-2xl font-mono text-[10px] text-slate-600 border border-slate-100">
                                    {`await fetch('${getBaseUrl()}/tables/users/data', {
  headers: { 'apikey': '${projectData?.anon_key}' }
});`}
                                </pre>
                            </div>
                        </div>
                    </div>

                    {/* Integrations Section */}
                    <div>
                        <h3 className="text-xl font-black text-slate-900 mb-6 flex items-center gap-2"><Blocks size={24} className="text-indigo-600" /> Low-Code Integrations</h3>
                        <div className="bg-indigo-600 text-white rounded-[2.5rem] p-10 shadow-2xl relative overflow-hidden">
                            <div className="absolute top-0 right-0 p-10 opacity-10"><Blocks size={180} /></div>
                            <div className="relative z-10 grid grid-cols-1 lg:grid-cols-2 gap-10">
                                <div>
                                    <h4 className="text-2xl font-black tracking-tight mb-2">Connect FlutterFlow & AppSmith</h4>
                                    <p className="text-indigo-100 text-sm font-medium mb-6">Use our Swagger/OpenAPI spec to instantly import all your tables and functions into low-code platforms.</p>
                                    <div className="flex gap-3">
                                        <a href="https://docs.flutterflow.io/data/api-calls/openapi-import" target="_blank" rel="noreferrer" className="bg-white/10 hover:bg-white/20 px-4 py-2 rounded-xl text-xs font-bold transition-all flex items-center gap-2"><LinkIcon size={12} /> FlutterFlow Docs</a>
                                        <a href="https://docs.appsmith.com/" target="_blank" rel="noreferrer" className="bg-white/10 hover:bg-white/20 px-4 py-2 rounded-xl text-xs font-bold transition-all flex items-center gap-2"><LinkIcon size={12} /> AppSmith Docs</a>
                                    </div>
                                </div>
                                <div className="bg-white/10 backdrop-blur-md rounded-2xl p-6 border border-white/20 space-y-4">
                                    <div>
                                        <label className="text-[10px] font-black uppercase tracking-widest text-indigo-200">Import URL (Swagger)</label>
                                        <div className="flex gap-2 mt-1">
                                            <code className="flex-1 bg-black/20 p-2 rounded-lg font-mono text-[10px] truncate">{getSwaggerUrl()}</code>
                                            <button onClick={() => safeCopyToClipboard(getSwaggerUrl(), 'swagger')} className="p-2 bg-white text-indigo-600 rounded-lg hover:bg-indigo-50"><Copy size={14} /></button>
                                        </div>
                                    </div>
                                    <div>
                                        <label className="text-[10px] font-black uppercase tracking-widest text-indigo-200">API Key Header</label>
                                        <div className="flex gap-2 mt-1">
                                            <code className="flex-1 bg-black/20 p-2 rounded-lg font-mono text-[10px] truncate">{projectData?.anon_key}</code>
                                            <button onClick={() => safeCopyToClipboard(projectData?.anon_key, 'apikey')} className="p-2 bg-white text-indigo-600 rounded-lg hover:bg-indigo-50"><Copy size={14} /></button>
                                        </div>
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            )}

            {/* GUIDES TAB */}
            {activeTab === 'guides' && (
                <div className="flex gap-10 h-[600px]">
                    <div className="w-64 shrink-0 flex flex-col gap-2">
                        <button onClick={() => setShowGenModal(true)} className="w-full bg-indigo-600 text-white py-3 rounded-xl font-black text-xs uppercase tracking-widest flex items-center justify-center gap-2 hover:bg-indigo-700 transition-all shadow-lg shadow-indigo-100 mb-4">
                            <Sparkles size={14} /> Write with AI
                        </button>
                        {guides.map(g => (
                            <button
                                key={g.id}
                                onClick={() => setSelectedGuide(g)}
                                className={`text-left px-4 py-3 rounded-xl text-sm font-bold transition-all ${selectedGuide?.id === g.id ? 'bg-white shadow-md text-indigo-600' : 'text-slate-500 hover:bg-white hover:shadow-sm'}`}
                            >
                                {g.title}
                            </button>
                        ))}
                        {guides.length === 0 && <p className="text-center text-slate-400 text-xs py-10 font-bold">No guides yet.</p>}
                    </div>
                    <div className="flex-1 bg-white border border-slate-200 rounded-[2.5rem] p-10 overflow-y-auto shadow-sm">
                        {selectedGuide ? (
                            <article className="prose prose-slate max-w-none">
                                <h1 className="text-3xl font-black tracking-tight mb-2">{selectedGuide.title}</h1>
                                <p className="text-xs text-slate-400 font-bold uppercase tracking-widest mb-8">Auto-Generated by Cascata Architect</p>
                                <div className="markdown-doc-content whitespace-pre-wrap font-medium text-slate-600 leading-relaxed text-sm">
                                    <div dangerouslySetInnerHTML={{ __html: marked.parse(selectedGuide.content_markdown) as string }} />
                                </div>
                            </article>
                        ) : (
                            <div className="h-full flex flex-col items-center justify-center text-slate-300 gap-4">
                                <FileText size={48} />
                                <span className="font-black uppercase tracking-widest text-xs">Select a guide</span>
                            </div>
                        )}
                    </div>
                </div>
            )}

            {/* EXECUTION RESULT MODAL */}
            {execModal && (
                <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[700] flex items-center justify-center p-6 animate-in zoom-in-95">
                    <div className="bg-white rounded-[2.5rem] w-full max-w-2xl shadow-2xl flex flex-col max-h-[90vh]">
                        {/* Header */}
                        <div className="flex items-center justify-between px-8 pt-8 pb-4 shrink-0">
                            <div className="flex items-center gap-3">
                                <div className={`w-8 h-8 rounded-xl flex items-center justify-center ${execModal.loading ? 'bg-slate-100' : execModal.error ? 'bg-rose-100' : execModal.status && execModal.status < 300 ? 'bg-emerald-100' : 'bg-amber-100'}`}>
                                    {execModal.loading
                                        ? <Loader2 size={16} className="animate-spin text-slate-500" />
                                        : execModal.error
                                            ? <AlertCircle size={16} className="text-rose-500" />
                                            : <Play size={16} className={execModal.status && execModal.status < 300 ? 'text-emerald-600 fill-emerald-600' : 'text-amber-600 fill-amber-600'} />
                                    }
                                </div>
                                <div>
                                    <h3 className="text-base font-black text-slate-900">Resultado</h3>
                                    {!execModal.loading && !execModal.error && (
                                        <span className={`text-[10px] font-black uppercase tracking-widest ${execModal.status && execModal.status < 300 ? 'text-emerald-600' : 'text-amber-600'}`}>
                                            {execModal.status} {execModal.statusText}
                                        </span>
                                    )}
                                </div>
                            </div>
                            <button onClick={() => setExecModal(null)} className="text-slate-400 hover:text-slate-700 text-[10px] font-black uppercase tracking-widest">Fechar</button>
                        </div>

                        <div className="overflow-y-auto flex-1 px-8 pb-8 space-y-4">
                            {execModal.loading && (
                                <div className="flex flex-col items-center justify-center py-16 text-slate-400 gap-3">
                                    <Loader2 size={32} className="animate-spin text-indigo-500" />
                                    <span className="text-xs font-black uppercase tracking-widest animate-pulse">Executando...</span>
                                </div>
                            )}

                            {execModal.error && (
                                <div className="bg-rose-50 border border-rose-200 rounded-2xl p-4 text-xs font-mono text-rose-700">{execModal.error}</div>
                            )}

                            {!execModal.loading && !execModal.error && (
                                <>
                                    {/* Response body */}
                                    <div>
                                        <span className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1 block mb-2">Response Body</span>
                                        <pre className="bg-slate-900 text-slate-300 p-5 rounded-2xl font-mono text-xs overflow-x-auto border border-slate-800 leading-relaxed max-h-64 overflow-y-auto">
                                            {execModal.responseBody || '(empty)'}
                                        </pre>
                                    </div>

                                    {/* Response headers */}
                                    {Object.keys(execModal.responseHeaders).length > 0 && (
                                        <div>
                                            <span className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1 block mb-2">Response Headers</span>
                                            <div className="bg-slate-50 border border-slate-200 rounded-2xl p-4 font-mono text-[10px] space-y-1 max-h-40 overflow-y-auto">
                                                {Object.entries(execModal.responseHeaders).map(([k, v]) => (
                                                    <div key={k}><span className="text-indigo-600 font-bold">{k}:</span> <span className="text-slate-600">{v}</span></div>
                                                ))}
                                            </div>
                                        </div>
                                    )}
                                </>
                            )}

                            {/* Editable curl — always shown so user can tweak and re-run */}
                            <div>
                                <span className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1 block mb-2">Curl Executado</span>
                                <textarea
                                    value={execModal.curl}
                                    onChange={e => setExecModal(prev => prev ? { ...prev, curl: e.target.value } : null)}
                                    spellCheck={false}
                                    className="w-full bg-slate-900 text-slate-300 p-5 rounded-2xl font-mono text-xs border border-slate-700 outline-none resize-none leading-relaxed"
                                    style={{ minHeight: '100px', height: `${Math.max(100, execModal.curl.split('\n').length * 20 + 40)}px` }}
                                />
                            </div>

                            <button
                                onClick={() => executeCurl(execModal.curl)}
                                className="w-full py-3 bg-slate-900 text-white rounded-2xl text-xs font-black uppercase tracking-widest flex items-center justify-center gap-2 hover:bg-indigo-600 transition-all"
                            >
                                <Play size={14} className="fill-current" /> Executar Novamente
                            </button>
                        </div>
                    </div>
                </div>
            )}

            {/* GENERATE GUIDE MODAL */}
            {showGenModal && (
                <div className="fixed inset-0 bg-slate-950/80 backdrop-blur-md z-[600] flex items-center justify-center p-8 animate-in zoom-in-95">
                    <div className="bg-white rounded-[3rem] w-full max-w-md p-10 shadow-2xl">
                        <h3 className="text-2xl font-black text-slate-900 mb-6 flex items-center gap-3"><Sparkles className="text-indigo-500" /> AI Technical Writer</h3>
                        <div className="space-y-4 mb-8">
                            <p className="text-xs text-slate-500 font-bold">Select a table to generate a comprehensive integration guide.</p>
                            <div className="grid grid-cols-2 gap-3 max-h-60 overflow-y-auto">
                                {tablesList.map(t => (
                                    <button key={t} onClick={() => handleGenerateDoc(t)} className="bg-slate-50 hover:bg-indigo-50 hover:text-indigo-700 text-slate-600 py-3 rounded-xl text-xs font-bold transition-all border border-slate-200 hover:border-indigo-200">
                                        {t}
                                    </button>
                                ))}
                            </div>
                        </div>
                        <button onClick={() => setShowGenModal(false)} className="w-full py-4 text-xs font-black text-slate-400 uppercase tracking-widest hover:text-slate-600">Cancel</button>
                        {generating && <div className="absolute inset-0 bg-white/90 flex flex-col items-center justify-center z-10 rounded-[3rem]"><Loader2 className="animate-spin text-indigo-600 mb-4" size={40} /><span className="text-xs font-black uppercase tracking-widest animate-pulse">Writing Documentation...</span></div>}
                    </div>
                </div>
            )}
            <style>{`
                .markdown-doc-content h1, .markdown-doc-content h2 { @apply font-black text-slate-900 mt-8 mb-4 border-b border-slate-100 pb-2; }
                .markdown-doc-content h3 { @apply font-bold text-slate-800 mt-6 mb-2 uppercase text-[10px] tracking-widest; }
                .markdown-doc-content p { margin-bottom: 1rem; }
                .markdown-doc-content ul, .markdown-doc-content ol { margin-bottom: 1.5rem; padding-left: 1.5rem; }
                .markdown-doc-content li { margin-bottom: 0.5rem; }
                .markdown-doc-content table { width: 100%; border-collapse: collapse; margin: 2rem 0; border: 1px solid #e2e8f0; border-radius: 1rem; overflow: hidden; }
                .markdown-doc-content th { background: #f8fafc; padding: 1rem; text-align: left; font-size: 10px; font-weight: 900; text-transform: uppercase; color: #64748b; }
                .markdown-doc-content td { padding: 1rem; border-bottom: 1px solid #f1f5f9; font-size: 12px; }
                .markdown-doc-content pre { background: #0f172a; color: #f1f5f9; padding: 1.5rem; border-radius: 1rem; margin: 1.5rem 0; overflow-x: auto; font-family: monospace; }
                .markdown-doc-content code { background: #f1f5f9; padding: 0.2rem 0.4rem; border-radius: 0.4rem; color: #4f46e5; font-weight: bold; }
                .markdown-doc-content pre code { background: transparent; padding: 0; color: inherit; }
            `}</style>
        </div>
    );
};

const CodeBlock: React.FC<{ label: string, code: string, onCopy: () => void, copied: boolean, onEdit?: (val: string) => void }> = ({ label, code, onCopy, copied, onEdit }) => {
    const [editing, setEditing] = React.useState(false);
    const [draft, setDraft] = React.useState('');
    const taRef = React.useRef<HTMLTextAreaElement>(null);

    const displayed = onEdit ? code : code; // code is always the source of truth; parent owns override

    const startEdit = () => {
        setDraft(code);
        setEditing(true);
        setTimeout(() => taRef.current?.focus(), 30);
    };

    const commitEdit = () => {
        setEditing(false);
        if (onEdit && draft !== code) onEdit(draft);
    };

    return (
        <div className="relative group">
            <div className="flex items-center justify-between mb-2">
                <span className="text-[10px] font-black text-slate-400 uppercase tracking-widest ml-1">{label}</span>
                <div className="flex items-center gap-3">
                    {editing ? (
                        <button onClick={commitEdit} className="text-emerald-500 text-[10px] font-bold uppercase hover:underline flex items-center gap-1">
                            <Check size={10} /> Done
                        </button>
                    ) : (
                        <button onClick={startEdit} className="text-slate-400 text-[10px] font-bold uppercase hover:underline flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                            <Terminal size={10} /> Edit
                        </button>
                    )}
                    <button onClick={onCopy} className="text-indigo-500 text-[10px] font-bold uppercase hover:underline flex items-center gap-1">
                        {copied ? <><Check size={10} /> Copied</> : <><Copy size={10} /> Copy</>}
                    </button>
                </div>
            </div>
            {editing ? (
                <textarea
                    ref={taRef}
                    value={draft}
                    onChange={e => setDraft(e.target.value)}
                    onBlur={commitEdit}
                    spellCheck={false}
                    className="w-full bg-slate-900 text-slate-300 p-6 rounded-2xl font-mono text-xs border border-indigo-500 shadow-inner leading-relaxed outline-none resize-none"
                    style={{ minHeight: '120px', height: `${Math.max(120, draft.split('\n').length * 20 + 48)}px` }}
                />
            ) : (
                <pre
                    onDoubleClick={startEdit}
                    title="Duplo-clique para editar"
                    className="bg-slate-900 text-slate-300 p-6 rounded-2xl font-mono text-xs overflow-x-auto border border-slate-800 shadow-inner leading-relaxed cursor-text select-text"
                >
                    {code}
                </pre>
            )}
        </div>
    );
};

const CrudExample: React.FC<{
    name: string;
    path: string;
    generateCurl: (method: string, path: string, type: 'table' | 'rpc' | 'edge') => string;
    safeCopyToClipboard: (text: string, id: string) => void;
    copiedUrl: string | null;
    richData: any;
    isParamsExpanded: boolean;
    onToggleParams: () => void;
    activeOp: string;
    selectedStepUpCode: string;
    setSelectedStepUpCode: (code: string) => void;
    handleStepUpCodeClick: (code: string) => void;
    onCurlEdit: (op: string, val: string) => void;
}> = ({ name, path, generateCurl, safeCopyToClipboard, copiedUrl, richData, isParamsExpanded, onToggleParams, activeOp, selectedStepUpCode, setSelectedStepUpCode, handleStepUpCodeClick, onCurlEdit }) => {
    const protectedFields = richData?.fields?.filter((f: any) => f.lockLevel === 'code_protected' || f.lockLevel === 'otp_protected') || [];
    const hasOtpProtected = protectedFields.length > 0;
    const tableStepUpFactors = getTableStepUpFactorsForMethod(richData, activeOp);
    const hasTableStepUp = tableStepUpFactors.length > 0;

    return (
        <div className="p-6 bg-slate-50/50 space-y-8 animate-in fade-in slide-in-from-top-2 duration-300">
            {/* Params Table (Collapsible) */}
            {richData?.fields && richData.fields.length > 0 && (
                <div className="bg-white rounded-xl border border-slate-200 overflow-hidden">
                    <div
                        onClick={onToggleParams}
                        className="px-4 py-2 bg-slate-100 border-b border-slate-200 flex justify-between items-center cursor-pointer hover:bg-slate-200/50 transition-colors"
                    >
                        <span className="text-[10px] font-black text-slate-500 uppercase tracking-widest flex items-center gap-2"><ListFilter size={12} /> Schema Fields</span>
                        {isParamsExpanded ? <ChevronDown size={14} className="text-slate-400" /> : <ChevronRight size={14} className="text-slate-400" />}
                    </div>
                    {isParamsExpanded && (
                        <div className="max-h-60 overflow-y-auto">
                            <table className="w-full text-left animate-in slide-in-from-top-1">
                                <tbody>
                                    {richData.fields.map((field: any, idx: number) => (
                                        <tr key={idx} className="border-b border-slate-100 last:border-0">
                                            <td className="px-4 py-2 text-xs font-bold font-mono text-indigo-700">{field.name}</td>
                                            <td className="px-4 py-2 text-xs font-mono text-slate-500">{field.type}</td>
                                            <td className="px-4 py-2 text-xs text-slate-400">
                                                {field.isPrimaryKey && <span className="bg-amber-100 text-amber-700 px-1.5 py-0.5 rounded text-[9px] font-black uppercase mr-1">PK</span>}
                                                {field.is_nullable === 'NO' && !field.column_default && !field.isPrimaryKey && <span className="bg-rose-100 text-rose-700 px-1.5 py-0.5 rounded text-[9px] font-black uppercase">REQ</span>}
                                                {/* Format Pattern Badge */}
                                                {field.formatPattern && (
                                                    <button
                                                        onClick={(e) => {
                                                            e.stopPropagation();
                                                            safeCopyToClipboard(field.formatPattern, `format-${field.name}-${idx}`);
                                                        }}
                                                        className="bg-emerald-100 hover:bg-emerald-200 text-emerald-700 active:scale-95 transition-all px-1.5 py-0.5 rounded text-[9px] font-black uppercase ml-1 inline-flex items-center gap-1 cursor-pointer border-0 shadow-sm"
                                                        title={`Click to copy regex pattern: ${field.formatPattern}`}
                                                    >
                                                        {copiedUrl === `format-${field.name}-${idx}` ? (
                                                            <>
                                                                <Check size={8} strokeWidth={3} />
                                                                COPIED!
                                                            </>
                                                        ) : (
                                                            <>
                                                                <Copy size={8} strokeWidth={3} />
                                                                FORMAT
                                                            </>
                                                        )}
                                                    </button>
                                                )}
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </div>
            )}

            {((hasOtpProtected && activeOp === 'PATCH') || hasTableStepUp) && (
                <div className="text-xs font-medium text-slate-500">
                    <span className="font-bold text-slate-600">Atenção:</span>{' '}
                    {hasTableStepUp ? (
                        <>
                            Esta operação exige validação extra na tabela com{' '}
                            {tableStepUpFactors.map((factor: string, idx: number) => (
                                <span key={factor}>
                                    <span
                                        className={`font-mono font-bold cursor-pointer hover:underline transition-colors ${selectedStepUpCode === `${factor}_code` ? 'text-indigo-600' : 'text-slate-700 hover:text-indigo-500'}`}
                                        onClick={() => handleStepUpCodeClick(`${factor}_code`)}
                                    >{factor}</span>
                                    {idx < tableStepUpFactors.length - 1 ? ', ' : ''}
                                </span>
                            ))}.
                        </>
                    ) : (
                        <>
                            Editar as colunas:{' '}
                            <span className="font-mono font-bold text-slate-700">
                                {protectedFields.map((f: any) => `${f.name} (${f.methods || f.Methods || 'TOTP/MFA, Passkey, OTP'})`).join(' e ')}
                            </span>{' '}
                            requer validação extra.
                        </>
                    )}{' '}
                    Envie{' '}
                    {(['totp_code', 'otp_code', 'passkey_code'] as const).map((code, i, arr) => (
                        <span key={code}>
                            <span
                                className={`font-mono font-bold cursor-pointer hover:underline transition-colors ${selectedStepUpCode === code ? 'text-indigo-600' : 'text-slate-700 hover:text-indigo-500'}`}
                                onClick={() => handleStepUpCodeClick(code)}
                            >{code}</span>
                            {i < arr.length - 2 ? ', ' : i === arr.length - 2 ? ' ou ' : '.'}
                        </span>
                    ))}
                </div>
            )}

            <div>
                {activeOp === 'GET' && (
                    <CodeBlock
                        label="List (GET)"
                        code={generateCurl('GET', path, 'table')}
                        onCopy={() => safeCopyToClipboard(generateCurl('GET', path, 'table'), `get-${name}`)}
                        copied={copiedUrl === `get-${name}`}
                        onEdit={val => onCurlEdit('GET', val)}
                    />
                )}
                {activeOp === 'POST' && (
                    <CodeBlock
                        label="Create (POST)"
                        code={generateCurl('POST', path, 'table')}
                        onCopy={() => safeCopyToClipboard(generateCurl('POST', path, 'table'), `post-${name}`)}
                        copied={copiedUrl === `post-${name}`}
                        onEdit={val => onCurlEdit('POST', val)}
                    />
                )}
                {activeOp === 'PATCH' && (
                    <CodeBlock
                        label="Update (PATCH)"
                        code={generateCurl('PATCH', path, 'table')}
                        onCopy={() => safeCopyToClipboard(generateCurl('PATCH', path, 'table'), `patch-${name}`)}
                        copied={copiedUrl === `patch-${name}`}
                        onEdit={val => onCurlEdit('PATCH', val)}
                    />
                )}
                {activeOp === 'DELETE' && (
                    <CodeBlock
                        label="Delete (DELETE)"
                        code={generateCurl('DELETE', path, 'table')}
                        onCopy={() => safeCopyToClipboard(generateCurl('DELETE', path, 'table'), `delete-${name}`)}
                        copied={copiedUrl === `delete-${name}`}
                        onEdit={val => onCurlEdit('DELETE', val)}
                    />
                )}
            </div>
        </div>
    );
};

export default APIDocs;