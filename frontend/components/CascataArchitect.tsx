
import React, { useState, useEffect, useRef } from 'react';
import {
  Sparkles, Send, Mic, MicOff, X,
  Terminal, Database, Play, Check, Loader2, Volume2,
  Copy, Maximize2, Move, Clock, ChevronLeft, Search, Edit2, Plus, Square, Trash2
} from 'lucide-react';
import { marked } from 'marked';

interface ArchitectProps {
  projectId: string;
}

// Helper robusto para gerar UUIDs em ambientes HTTP/HTTPS
const getUUID = () => {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    try { return crypto.randomUUID(); } catch(e) { /* ignore */ }
  }
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
};

const CascataArchitect: React.FC<ArchitectProps> = ({ projectId }) => {
  const [isOpen, setIsOpen] = useState(false);
  const [messages, setMessages] = useState<{ role: 'user' | 'assistant', content: string, type?: 'text' | 'sql' | 'json', actionData?: any }[]>([]);
  const [input, setInput] = useState('');
  const [isListening, setIsListening] = useState(false);
  const [isProcessing, setIsProcessing] = useState(false);
  const [sessionId, setSessionId] = useState('');
  
  // VIEW MODE: 'chat' or 'history'
  const [viewMode, setViewMode] = useState<'chat' | 'history'>('chat');
  const [sessions, setSessions] = useState<any[]>([]);
  const [searchQuery, setSearchQuery] = useState('');
  const [editingTitleId, setEditingTitleId] = useState<string | null>(null);
  const [tempTitle, setTempTitle] = useState('');

  // AI Config State
  const [aiSettings, setAiSettings] = useState<any>({
    active_listening: false,
    wake_word: 'Cascata',
    transcription_url: '',
    tts_url: '',
    enable_streaming: false,
    response_mode: 'chat_completions',
    execute_word: 'Executar',
    new_chat_word: 'Novo Chat'
  });
  const [isActiveListeningMode, setIsActiveListeningMode] = useState(false);
  const [isWakeWordDetected, setIsWakeWordDetected] = useState(false);

  // Voice State Machine
  type VoiceState = 'IDLE_WAKEWORD' | 'LISTENING_COMMAND' | 'PROCESSING' | 'SPEAKING_RESPONSE';
  const [voiceState, setVoiceState] = useState<VoiceState>('IDLE_WAKEWORD');
  const voiceStateRef = useRef<VoiceState>('IDLE_WAKEWORD');

  // Resize State
  const [dimensions, setDimensions] = useState({ width: 400, height: 600 });
  const [isResizing, setIsResizing] = useState(false);

  // Message Selection State
  const [selectionMode, setSelectionMode] = useState(false);
  const [selectedIndices, setSelectedIndices] = useState<Set<number>>(new Set());
  const longPressTimer = useRef<any>(null);

  // Voice Recognition Setup
  const recognitionRef = useRef<any>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const silenceTimer = useRef<any>(null);
  const transcriptBuffer = useRef<string>(''); 
  const inputRef = useRef<string>(''); 
  const isSpeakingRef = useRef<boolean>(false);
  const isConversationModeRef = useRef<boolean>(false);
  const conversationIdleTimer = useRef<any>(null);
  
  // MediaRecorder for cloud transcription
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  const microphoneStreamRef = useRef<MediaStream | null>(null);
  
  // Command listening timeout (6s)
  const commandTimeoutRef = useRef<any>(null);
  
  // Prevent duplicate sends
  const isSendingRef = useRef<boolean>(false);
  const lastSentMessageRef = useRef<string>('');
  const sendSequenceRef = useRef<number>(0);
  
  // Track input source: 'keyboard' | 'voice'
  const inputSourceRef = useRef<'keyboard' | 'voice'>('keyboard');

  // AudioContext persistente para evitar erro de gesto do usuário
  const audioContextRef = useRef<AudioContext | null>(null);
  const keepAliveNodeRef = useRef<OscillatorNode | null>(null);

  // Ref para controle de auto-restart do microfone
  const restartTimeoutRef = useRef<any>(null);
  const lastAudioActivityRef = useRef<number>(Date.now());

  // Sync ref with state
  useEffect(() => { inputRef.current = input; }, [input]);

  const setVoiceStateSafe = (newState: VoiceState) => {
    voiceStateRef.current = newState;
    setVoiceState(newState);
  };

  // Message Selection Handlers
  const startSelectionMode = (index: number) => {
    setSelectionMode(true);
    setSelectedIndices(new Set([index]));
  };

  const toggleMessageSelection = (index: number) => {
    setSelectedIndices(prev => {
      const newSet = new Set(prev);
      if (newSet.has(index)) {
        newSet.delete(index);
      } else {
        newSet.add(index);
      }
      if (newSet.size === 0) {
        setSelectionMode(false);
      }
      return newSet;
    });
  };

  const exitSelectionMode = () => {
    setSelectionMode(false);
    setSelectedIndices(new Set());
  };

  const deleteSelectedMessages = async () => {
    const indicesToDelete = Array.from(selectedIndices);
    for (const idx of indicesToDelete) {
      const msg = messages[idx];
      if (msg && msg.id) {
        await deleteMessage(msg.id);
      }
    }
    exitSelectionMode();
  };

  const handleMessageMouseDown = (index: number) => {
    if (selectionMode) return;
    longPressTimer.current = setTimeout(() => {
      startSelectionMode(index);
    }, 500);
  };

  const handleMessageMouseUp = () => {
    if (longPressTimer.current) {
      clearTimeout(longPressTimer.current);
      longPressTimer.current = null;
    }
  };

  const handleMessageClick = (index: number) => {
    if (!selectionMode) return;
    toggleMessageSelection(index);
  };

  // Handle Resize
  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (!isResizing) return;
      const newWidth = window.innerWidth - e.clientX - 32; 
      const newHeight = window.innerHeight - e.clientY - 32;
      setDimensions({
        width: Math.max(300, Math.min(newWidth, 800)),
        height: Math.max(400, Math.min(newHeight, 900))
      });
    };
    const handleMouseUp = () => setIsResizing(false);
    
    if (isResizing) {
        document.addEventListener('mousemove', handleMouseMove);
        document.addEventListener('mouseup', handleMouseUp);
    }
    return () => {
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isResizing]);

  // Initialize Session & Config
  useEffect(() => {
    let storedSession = localStorage.getItem(`ai_session_${projectId}`);
    if (!storedSession) {
      startNewSession();
    } else {
        setSessionId(storedSession);
        loadHistory(storedSession);
    }
    loadConfig();
  }, [projectId]);

  useEffect(() => {
    if (scrollRef.current) {
        scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages, isOpen, viewMode]);

  // Detectar quando usuário volta à página e reativar microfone se necessário
  useEffect(() => {
      const handleVisibilityChange = () => {
          if (!document.hidden && audioContextRef.current?.state === 'suspended') {
              audioContextRef.current.resume();
          }
          if (document.visibilityState === 'visible' && isActiveListeningMode && voiceStateRef.current === 'IDLE_WAKEWORD') {
              console.log(`%c[Cascata Architect] Page visible - restarting wake word listening`, 'color: #10b981; font-weight: bold;');
              destroyRecognition();
              startIdleWakeWordListening();
          }
      };
      document.addEventListener('visibilitychange', handleVisibilityChange);
      return () => document.removeEventListener('visibilitychange', handleVisibilityChange);
  }, [isActiveListeningMode]);

  // --- SESSION MANAGEMENT ---

  const startNewSession = () => {
      const newId = getUUID();
      localStorage.setItem(`ai_session_${projectId}`, newId);
      setSessionId(newId);
      setMessages([]);
      setViewMode('chat');
  };

  const loadSessions = async () => {
      try {
          const res = await fetch(`/api/data/${projectId}/ai/sessions`, {
              headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
          });
          const data = await res.json();
          setSessions(Array.isArray(data) ? data : []);
      } catch (e) { console.error("Failed to load sessions"); }
  };

  const performSearch = async () => {
      if (!searchQuery) { loadSessions(); return; }
      try {
          const res = await fetch(`/api/data/${projectId}/ai/sessions/search`, {
              method: 'POST',
              headers: { 
                  'Content-Type': 'application/json',
                  'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` 
              },
              body: JSON.stringify({ query: searchQuery })
          });
          const data = await res.json();
          setSessions(Array.isArray(data) ? data : []);
      } catch (e) { console.error("Search failed"); }
  };

  const handleRenameSession = async (id: string) => {
      if (!tempTitle.trim()) return;
      try {
          await fetch(`/api/data/${projectId}/ai/sessions/${id}`, {
              method: 'PATCH',
              headers: { 
                  'Content-Type': 'application/json',
                  'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` 
              },
              body: JSON.stringify({ title: tempTitle })
          });
          setSessions(prev => prev.map(s => s.id === id ? { ...s, title: tempTitle } : s));
          setEditingTitleId(null);
      } catch(e) { alert("Erro ao renomear."); }
  };

  const loadConfig = async () => {
      try {
          const res = await fetch('/api/control/system/settings', {
              headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` }
          });
          const data = await res.json();
          if (data.ai_config) {
              setAiSettings(data.ai_config);
              setIsActiveListeningMode(data.ai_config.active_listening);
              if (data.ai_config.active_listening) {
                  startIdleWakeWordListening();
              }
          }
      } catch (e) {}
  };

  const loadHistory = async (sid: string) => {
    try {
        const token = localStorage.getItem('cascata_token');
        const res = await fetch(`/api/data/${projectId}/ai/history/${sid}`, {
            headers: { 'Authorization': `Bearer ${token}` }
        });
        const data = await res.json();
        const formatted = data.map((msg: any) => ({
            id: msg.id,
            role: msg.role,
            content: msg.content || '',
            type: (msg.content || '').includes('"action": "create_table"') ? 'json' : (msg.content || '').includes('```sql') ? 'sql' : 'text',
            actionData: (msg.content || '').includes('"action": "create_table"') ? extractJSON(msg.content) : null
        }));
        setMessages(formatted);
    } catch (e) { console.error("History load failed"); }
  };

  const updateMessage = async (messageId: string, newContent: string) => {
    try {
        const token = localStorage.getItem('cascata_token');
        const res = await fetch(`/api/data/${projectId}/ai/history/${messageId}`, {
            method: 'PATCH',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${token}`
            },
            body: JSON.stringify({ content: newContent })
        });
        if (res.ok) {
            setMessages(prev => prev.map(m => m.id === messageId ? { ...m, content: newContent } : m));
        }
    } catch (e) { console.error("Update message failed", e); }
  };

  const deleteMessage = async (messageId: string) => {
    try {
        const token = localStorage.getItem('cascata_token');
        const res = await fetch(`/api/data/${projectId}/ai/history/${messageId}`, {
            method: 'DELETE',
            headers: { 'Authorization': `Bearer ${token}` }
        });
        if (res.ok) {
            setMessages(prev => prev.filter(m => m.id !== messageId));
        }
    } catch (e) { console.error("Delete message failed", e); }
  };

  const deleteSession = async (sessionIdToDelete: string) => {
    console.log('[Cascata] Deleting session:', sessionIdToDelete);
    try {
        const token = localStorage.getItem('cascata_token');
        const url = `/api/data/${projectId}/ai/sessions/${sessionIdToDelete}`;
        console.log('[Cascata] DELETE URL:', url);
        const res = await fetch(url, {
            method: 'DELETE',
            headers: { 'Authorization': `Bearer ${token}` }
        });
        console.log('[Cascata] Delete response:', res.status);
        if (res.ok) {
            setSessions(prev => prev.filter((s: any) => s.id !== sessionIdToDelete));
            // Se deletou a sessão atual, inicia nova
            if (sessionIdToDelete === sessionId) {
                console.log('[Cascata] Deleted current session, starting new');
                startNewSession();
            }
        } else {
            const err = await res.text();
            console.error('[Cascata] Delete failed:', err);
        }
    } catch (e) { console.error("[Cascata] Delete session failed", e); }
  };

  const extractJSON = (text: string) => {
      if (!text) return null;
      try {
          const match = text.match(/```json\n([\s\S]*?)\n```/) || text.match(/{[\s\S]*}/);
          if (match) return JSON.parse(match[1] || match[0]);
      } catch (e) { return null; }
      return null;
  };

  const ensureAudioContext = () => {
      if (audioContextRef.current && audioContextRef.current.state !== 'closed') {
          if (audioContextRef.current.state === 'suspended') {
              audioContextRef.current.resume();
          }
          return audioContextRef.current;
      }
      const ctx = new (window.AudioContext || (window as any).webkitAudioContext)();

      // Oscillator mudo — mantém o contexto vivo e impede o Chrome de desligar
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      gain.gain.value = 0; // sem som
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start();

      audioContextRef.current = ctx;
      keepAliveNodeRef.current = osc;
      return ctx;
  };

  const playPing = () => {
      // Se não tem contexto ainda (sem gesto do usuário), silencia
      if (!audioContextRef.current || audioContextRef.current.state === 'closed') return;
      try {
          const ctx = ensureAudioContext(); // reutiliza o contexto existente
          const oscillator = ctx.createOscillator();
          const gainNode = ctx.createGain();
          oscillator.connect(gainNode);
          gainNode.connect(ctx.destination);
          oscillator.type = 'sine';
          oscillator.frequency.setValueAtTime(880, ctx.currentTime);
          gainNode.gain.setValueAtTime(0.1, ctx.currentTime);
          oscillator.start();
          oscillator.stop(ctx.currentTime + 0.15);
      } catch(e) {}
  };

  // Destroy recognition instance completely (REGRA 1)
  const destroyRecognition = () => {
      if (recognitionRef.current) {
          try { recognitionRef.current.stop(); } catch(e){}
          recognitionRef.current.onresult = null;
          recognitionRef.current.onerror = null;
          recognitionRef.current.onend = null;
          recognitionRef.current = null;
      }
      if (silenceTimer.current) { clearTimeout(silenceTimer.current); silenceTimer.current = null; }
      if (commandTimeoutRef.current) { clearTimeout(commandTimeoutRef.current); commandTimeoutRef.current = null; }
      if (restartTimeoutRef.current) { clearTimeout(restartTimeoutRef.current); restartTimeoutRef.current = null; }
  };

  const stopMicrophoneStream = () => {
      if (microphoneStreamRef.current) {
          microphoneStreamRef.current.getTracks().forEach((track: MediaStreamTrack) => track.stop());
          microphoneStreamRef.current = null;
      }
  };

  const restartMicrophoneStream = async (): Promise<MediaStream | null> => {
      // Para o stream antigo completamente
      stopMicrophoneStream();
      // Pequena pausa para garantir que o hardware seja liberado
      await new Promise(r => setTimeout(r, 100));
      try {
          const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
          microphoneStreamRef.current = stream;
          console.log(`%c[Cascata Architect] Microphone physically restarted`, 'color: #10b981; font-weight: bold;');
          return stream;
      } catch (e) {
          console.error('Microphone restart failed:', e);
          return null;
      }
  };

  const ensureMicrophoneStream = async (): Promise<MediaStream | null> => {
      if (microphoneStreamRef.current) return microphoneStreamRef.current;
      try {
          const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
          microphoneStreamRef.current = stream;
          return stream;
      } catch (e) { console.error('Mic denied'); return null; }
  };

  const createRecognition = () => {
      if (!('webkitSpeechRecognition' in window)) return null;
      const rec = new (window as any).webkitSpeechRecognition();
      rec.continuous = true; rec.interimResults = true; rec.lang = 'pt-BR';
      return rec;
  };

  const interruptSpeakingForUserInput = () => {
      if (!('speechSynthesis' in window)) return;
      if (window.speechSynthesis.speaking || isSpeakingRef.current) {
          window.speechSynthesis.cancel();
          isSpeakingRef.current = false;
      }
  };

  const getAdaptiveSilenceTimeout = (currentText: string) => {
      const words = currentText.trim().split(/\s+/).filter(Boolean).length;
      const timeout = 1500 + words * 140;
      return Math.max(1500, Math.min(timeout, 4500));
  };

  const buildContextMessages = (allMessages: { role: 'user' | 'assistant'; content: string }[], newestUserMessage: { role: 'user'; content: string }) => {
      const merged = [...allMessages, newestUserMessage];
      const hardMaxChars = 14000; // estimativa simples para evitar explosão de tokens
      const out: { role: 'user' | 'assistant'; content: string }[] = [];
      let totalChars = 0;
      for (let i = merged.length - 1; i >= 0; i--) {
          const msg = merged[i];
          totalChars += msg.content.length;
          out.unshift(msg);
          if (out.length >= 20 || totalChars >= hardMaxChars) break;
      }
      return out;
  };

  // Sistema de keepalive: força restart se não detectar áudio por 45s
  const scheduleForcedRestart = () => {
      if (restartTimeoutRef.current) clearTimeout(restartTimeoutRef.current);
      restartTimeoutRef.current = setTimeout(() => {
          const silenceDuration = Date.now() - lastAudioActivityRef.current;
          if (silenceDuration > 45000 && voiceStateRef.current === 'IDLE_WAKEWORD' && isActiveListeningMode) {
              console.log(`%c[Cascata Architect] Forced restart: no audio activity for ${silenceDuration}ms`, 'color: #f59e0b; font-weight: bold;');
              destroyRecognition();
              startIdleWakeWordListening();
          }
      }, 45000);
  };

  // STATE: IDLE_WAKEWORD
  const startIdleWakeWordListening = async () => {
      if (!('webkitSpeechRecognition' in window)) return;
      destroyRecognition();
      // Reinicia o microfone fisicamente no navegador - garante que não está suspenso
      await restartMicrophoneStream();
      setVoiceStateSafe('IDLE_WAKEWORD');
      setIsWakeWordDetected(false);
      isConversationModeRef.current = false;
      transcriptBuffer.current = '';
      lastAudioActivityRef.current = Date.now();

      const recognition = createRecognition();
      if (!recognition) return;

      recognition.onresult = (event: any) => {
          // Atualiza timestamp de atividade a cada resultado (interim ou final)
          lastAudioActivityRef.current = Date.now();
          scheduleForcedRestart();

          let heardAnyFinalSpeech = false;
          for (let i = event.resultIndex; i < event.results.length; ++i) {
              if (!event.results[i].isFinal) continue;
              heardAnyFinalSpeech = true;
              const transcript = event.results[i][0].transcript.trim();
              const wakeWord = (aiSettings.wake_word || 'Cascata').toLowerCase();
              const execWord = (aiSettings.execute_word || '').toLowerCase();
              const newChatWord = (aiSettings.new_chat_word || '').toLowerCase();
              
              if (execWord && execWord.trim() !== '') {
                  const execRegex = new RegExp(`\\b${execWord.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\b`, 'i');
                  if (execRegex.test(transcript)) {
                      console.log(`%c[Cascata Architect] Voice Wake: EXECUTE keyword detected`, 'color: #f59e0b; font-weight: bold;', { transcript });
                      interruptSpeakingForUserInput();
                      destroyRecognition();
                      autoExecutePendingBlocks();
                      return;
                  }
              }

              if (newChatWord && newChatWord.trim() !== '') {
                  const newChatRegex = new RegExp(`\\b${newChatWord.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\b`, 'i');
                  if (newChatRegex.test(transcript)) {
                      console.log(`%c[Cascata Architect] Voice Wake: NEW CHAT keyword detected`, 'color: #f59e0b; font-weight: bold;', { transcript });
                      interruptSpeakingForUserInput();
                      destroyRecognition();
                      startNewSession();
                      transitionToListeningCommand('');
                      return;
                  }
              }

              const wakeRegex = new RegExp(`\\b${wakeWord.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\b`, 'i');
              if (wakeRegex.test(transcript)) {
                  console.log(`%c[Cascata Architect] Voice Wake: WAKE WORD detected`, 'color: #6366f1; font-weight: bold;', { transcript });
                  interruptSpeakingForUserInput();
                  const match = wakeRegex.exec(transcript);
                  const after = match ? transcript.slice(match.index + match[0].length).trim() : '';
                  destroyRecognition();
                  transitionToListeningCommand(after);
                  return;
              }
          }
          if (heardAnyFinalSpeech) interruptSpeakingForUserInput();
      };

      recognition.onerror = (e: any) => {
          // 'no-speech' é normal quando fica em silêncio - Chrome desliga, reiniciamos no onend
          // 'aborted' é quando nós mesmos abortamos - ignora
          if (e.error === 'no-speech' || e.error === 'aborted') {
              return;
          }
          console.warn(`%c[Cascata Architect] Speech error: ${e.error}`, 'color: #ef4444;');
          if (!isSpeakingRef.current && voiceStateRef.current === 'IDLE_WAKEWORD') {
              restartTimeoutRef.current = setTimeout(() => {
                  if (voiceStateRef.current === 'IDLE_WAKEWORD') startIdleWakeWordListening();
              }, 300);
          }
      };

      recognition.onend = () => {
          // Chrome sempre dispara onend após um tempo de silêncio ou no-speech
          // Se ainda estamos em IDLE_WAKEWORD, reiniciamos imediatamente
          if (!isSpeakingRef.current && voiceStateRef.current === 'IDLE_WAKEWORD') {
              restartTimeoutRef.current = setTimeout(() => {
                  if (voiceStateRef.current === 'IDLE_WAKEWORD') startIdleWakeWordListening();
              }, 100);
          }
      };

      try {
          recognition.start();
          recognitionRef.current = recognition;
          scheduleForcedRestart(); // Inicia o keepalive
      } catch(e) {
          restartTimeoutRef.current = setTimeout(() => startIdleWakeWordListening(), 500);
      }
  };

  // STATE: LISTENING_COMMAND
  const transitionToListeningCommand = async (preliminaryText: string = '') => {
      if (!('webkitSpeechRecognition' in window)) return;
      interruptSpeakingForUserInput();
      // Garante que o microfone esteja fisicamente ativo para o modo de comando
      await restartMicrophoneStream();
      setVoiceStateSafe('LISTENING_COMMAND');
      setIsWakeWordDetected(true);
      setIsOpen(true);
      isConversationModeRef.current = true;
      playPing();
      transcriptBuffer.current = '';
      if (preliminaryText) {
          console.log(`%c[Cascata Architect] Preliminary text carried over from wake word`, 'color: #8b5cf6; font-weight: bold;', { preliminaryText });
          transcriptBuffer.current = preliminaryText + ' ';
          setInput(preliminaryText);
      }

      const hasTranscriptionUrl = aiSettings.transcription_url && aiSettings.transcription_url.trim() !== '';
      if (hasTranscriptionUrl) {
          const stream = await ensureMicrophoneStream();
          if (stream) {
              audioChunksRef.current = [];
              try {
                  const recorder = new MediaRecorder(stream);
                  recorder.ondataavailable = (e: BlobEvent) => { if (e.data.size > 0) audioChunksRef.current.push(e.data); };
                  recorder.start(100);
                  mediaRecorderRef.current = recorder;
              } catch(e) {}
          }
      }

      const recognition = createRecognition();
      if (!recognition) return;

      commandTimeoutRef.current = setTimeout(() => {
          // CORREÇÃO: Se já tem texto no buffer (ex: veio do wake word), finaliza em vez de descartar
          const pendingText = transcriptBuffer.current.trim();
          if (pendingText) {
              console.log(`%c[Cascata Architect] Command timeout with pending text — finalizing`, 'color: #f59e0b; font-weight: bold;', { pendingText });
              finalizeVoiceCommand(pendingText);
          } else {
              destroyRecognition(); stopMediaRecorder();
              setIsWakeWordDetected(false); isConversationModeRef.current = false;
              setInput(''); setVoiceStateSafe('IDLE_WAKEWORD');
              if (isActiveListeningMode) startIdleWakeWordListening();
          }
      }, 6000);

      recognition.onresult = (event: any) => {
          if (silenceTimer.current) clearTimeout(silenceTimer.current);
          interruptSpeakingForUserInput();
          for (let i = event.resultIndex; i < event.results.length; ++i) {
              if (!event.results[i].isFinal) continue;
              transcriptBuffer.current += event.results[i][0].transcript.trim() + ' ';
              setInput(transcriptBuffer.current.trim());
          }
          if (commandTimeoutRef.current) { clearTimeout(commandTimeoutRef.current); }
          const timeout = getAdaptiveSilenceTimeout(transcriptBuffer.current);
          silenceTimer.current = setTimeout(() => {
              const txt = transcriptBuffer.current.trim();
              if (txt) finalizeVoiceCommand(txt);
          }, timeout);
      };

      recognition.onerror = () => {
          const txt = transcriptBuffer.current.trim();
          if (txt) finalizeVoiceCommand(txt);
          else { destroyRecognition(); stopMediaRecorder(); setIsWakeWordDetected(false); isConversationModeRef.current = false; setInput(''); setVoiceStateSafe('IDLE_WAKEWORD'); if (isActiveListeningMode) startIdleWakeWordListening(); }
      };

      recognition.onend = () => {
          const txt = transcriptBuffer.current.trim();
          if (txt && voiceStateRef.current === 'LISTENING_COMMAND') finalizeVoiceCommand(txt);
      };

      try { recognition.start(); recognitionRef.current = recognition; }
      catch(e) { if (preliminaryText) finalizeVoiceCommand(preliminaryText); }

      // CORREÇÃO PRINCIPAL: Se já veio texto preliminar do wake word,
      // inicia o silence timer imediatamente. Se o usuário não falar mais nada
      // dentro de 1.8s, o texto preliminar é finalizado e enviado.
      if (preliminaryText && preliminaryText.trim()) {
          const timeout = getAdaptiveSilenceTimeout(preliminaryText);
          silenceTimer.current = setTimeout(() => {
              const txt = transcriptBuffer.current.trim();
              if (txt) {
                  console.log(`%c[Cascata Architect] Preliminary text silence timer fired — finalizing`, 'color: #8b5cf6; font-weight: bold;', { txt });
                  finalizeVoiceCommand(txt);
              }
          }, timeout);
      }
  };

  const stopMediaRecorder = () => {
      if (mediaRecorderRef.current && mediaRecorderRef.current.state !== 'inactive') {
          try { mediaRecorderRef.current.stop(); } catch(e){}
      }
      mediaRecorderRef.current = null;
  };

  const getAudioBlob = (): Blob | null => {
      if (audioChunksRef.current.length === 0) return null;
      return new Blob(audioChunksRef.current, { type: 'audio/webm' });
  };

  const finalizeVoiceCommand = async (localTranscript: string) => {
      console.log(`%c[Cascata Architect] Finalizing Voice Command`, 'color: #8b5cf6; font-weight: bold;', { localTranscript, timestamp: new Date().toISOString() });
      destroyRecognition();
      setVoiceStateSafe('PROCESSING');
      setIsProcessing(true);
      inputSourceRef.current = 'voice';

      let finalText = localTranscript;
      const blob = getAudioBlob();
      stopMediaRecorder();

      // Try cloud transcription if configured
      if (blob && aiSettings.transcription_url) {
          try {
              const formData = new FormData();
              formData.append('audio', blob, 'command.webm');
              const res = await fetch(aiSettings.transcription_url, { method: 'POST', body: formData, headers: { 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` } });
              if (res.ok) {
                  const data = await res.json();
                  if (data.text) finalText = data.text;
              }
          } catch(e) { /* fallback to local transcript */ }
      }

      setInput(finalText);
      await handleSendVoice(finalText);
  };

  // --- CONVERSATION MODE MANAGEMENT ---

  const exitConversationMode = () => {
      destroyRecognition();
      stopMediaRecorder();
      isConversationModeRef.current = false;
      setIsWakeWordDetected(false);
      transcriptBuffer.current = '';
      setInput('');
      setVoiceStateSafe('IDLE_WAKEWORD');
      if (conversationIdleTimer.current) { clearTimeout(conversationIdleTimer.current); conversationIdleTimer.current = null; }
      // Reinicia o microfone para continuar ouvindo após sair do modo conversação
      if (isActiveListeningMode) startIdleWakeWordListening();
  };

  const resetConversationIdleTimer = () => {
      if (conversationIdleTimer.current) clearTimeout(conversationIdleTimer.current);
      conversationIdleTimer.current = setTimeout(() => { exitConversationMode(); }, 12000);
  };

  const toggleListening = () => {
      ensureAudioContext(); // ancora o contexto no gesto
      if (isActiveListeningMode) {
          if (isConversationModeRef.current || voiceStateRef.current === 'LISTENING_COMMAND') {
              exitConversationMode();
          } else {
              transitionToListeningCommand('');
          }
      } else {
          if (isListening || voiceStateRef.current === 'LISTENING_COMMAND') {
              destroyRecognition();
              stopMediaRecorder();
              setIsListening(false);
              setVoiceStateSafe('IDLE_WAKEWORD');
          } else {
              setInput('');
              inputSourceRef.current = 'voice';
              transitionToListeningCommand('');
              setIsListening(true);
          }
      }
  };

  const speakText = (text: string) => {
      const isVoiceInput = inputSourceRef.current === 'voice';
      // REGRA 8: Se input foi voz, TTS fala; se teclado, não fala
      if (!isVoiceInput) return;

      // Se tts_url configurado, enviar para endpoint IA TTS
      if (aiSettings.tts_url && aiSettings.tts_url.trim() !== '') {
          // TTS via endpoint externo (não bloqueia, apenas envia)
          fetch(aiSettings.tts_url, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${localStorage.getItem('cascata_token')}` },
              body: JSON.stringify({ text })
          }).catch(() => {});
          // Em paralelo, fala nativo como fallback
      }

      if (!('speechSynthesis' in window)) return;

      // Sanitização completa para fala natural
      let spokenText = text;
      spokenText = spokenText.replace(/```[\s\S]*?```/g, ' Consulte o código no chat. ');
      spokenText = spokenText.replace(/`[^`]+`/g, ' Consulte o código no chat. ');
      spokenText = spokenText.replace(/https?:\/\/[^\s)]+/g, ' endereço no chat ');
      spokenText = spokenText.replace(/^#{1,6}\s*/gm, '');
      spokenText = spokenText.replace(/\*{1,3}/g, '');
      spokenText = spokenText.replace(/^\s*[-*]\s+/gm, '');
      spokenText = spokenText.replace(/^\s*\d+\.\s+/gm, '');
      spokenText = spokenText.replace(/[\u{1F600}-\u{1F64F}\u{1F300}-\u{1F5FF}\u{1F680}-\u{1F6FF}\u{2600}-\u{26FF}\u{2700}-\u{27BF}]/gu, '');
      spokenText = spokenText.replace(/\n+/g, '. ').replace(/\s{2,}/g, ' ').trim();
      spokenText = spokenText.replace(/(Consulte o código no chat\.\s*){2,}/g, 'Consulte o código no chat. ');

      if (!spokenText.trim()) return;

      setVoiceStateSafe('SPEAKING_RESPONSE');
      isSpeakingRef.current = true;
      window.speechSynthesis.cancel();

      const utterance = new SpeechSynthesisUtterance(spokenText);
      utterance.lang = 'pt-BR';
      utterance.rate = 1.05;
      utterance.pitch = 1.0;
      
      utterance.onend = () => {
          isSpeakingRef.current = false;
          // REGRA 9: Depois de TTS, destruir e recriar recognition após 400ms
          setTimeout(() => {
              destroyRecognition();
              setVoiceStateSafe('IDLE_WAKEWORD');
              if (isActiveListeningMode) startIdleWakeWordListening();
          }, 400);
      };

      utterance.onerror = () => {
          isSpeakingRef.current = false;
          setTimeout(() => {
              destroyRecognition();
              setVoiceStateSafe('IDLE_WAKEWORD');
              if (isActiveListeningMode) startIdleWakeWordListening();
          }, 400);
      };

      window.speechSynthesis.speak(utterance);
  };

  const stopSpeaking = () => {
      window.speechSynthesis.cancel();
      isSpeakingRef.current = false;
      setVoiceStateSafe('IDLE_WAKEWORD');
      if (isActiveListeningMode) startIdleWakeWordListening();
  };

  const handleSendVoice = async (textToSend: string) => {
      await handleSendCore(textToSend, 'voice');
  };

  const handleSend = async (e?: any) => {
      if (e) e.preventDefault();
      inputSourceRef.current = 'keyboard';
      const trimmedText = input.trim();
      if (!trimmedText) return;
      await handleSendCore(trimmedText, 'keyboard');
  };

  // --- BACKEND NAVIGATION FALLBACK HANDLER ---
  // Detecta se a resposta da IA contém instruções de navegação que passaram despercebidas pelo frontend.
  // Se a IA devolveu um JSON com action=navigation_fallback, o frontend obedece e navega.
  const handleBackendNavigationFallback = (responseContent: string): boolean => {
      try {
          // Tenta extrair JSON de navigation_fallback do conteúdo
          const navMatch = responseContent.match(/\{[^{}]*"action"\s*:\s*"navigation_fallback"[^{}]*\}/);
          if (!navMatch) return false;
          
          const fb = JSON.parse(navMatch[0]);
          console.log(`%c[Cascata Architect] Backend Fallback Navigation Detected in AI Response!`, 'color: #ef4444; font-weight: bold;', fb);

          if (fb.target_type === 'table' && fb.table_name) {
              localStorage.setItem('cascata_pending_table_selection', fb.table_name);
              window.location.hash = `#/project/${projectId}/database`;
          } else if (fb.target_route) {
              if (fb.is_global) {
                  window.location.hash = `#/${fb.target_route}`;
              } else {
                  window.location.hash = `#/project/${projectId}/${fb.target_route}`;
              }
          } else {
              return false; // Não era um fallback válido
          }

          const navMsg = fb.spoken_feedback || `Navegando para ${fb.label || 'a tela solicitada'}`;
          setMessages(prev => [...prev, { role: 'assistant', content: navMsg, type: 'text' }]);
          speakText(navMsg);
          return true;
      } catch {
          return false;
      }
  };

  const handleSendCore = async (textToSend: string, source: 'keyboard' | 'voice') => {
    // Prevent duplicate sends
    if (isSendingRef.current) return;
    
    // In History Mode, Input acts as Search
    if (viewMode === 'history') {
        performSearch();
        return;
    }

    const trimmedText = textToSend.trim();
    if (!trimmedText) return;

    console.log(`%c[Cascata Architect] Input Received`, 'color: #6366f1; font-weight: bold;', { text: trimmedText, source, timestamp: new Date().toISOString() });

    // --- BLOCO DE SEGURANÇA INTELIGENTE ---
    // Impede chamadas acidentais à API se a mensagem for EXATAMENTE a palavra de wake, execução ou novo chat
    // ou uma combinação (ex: "Cascata novo chat").
    const rawWake = (aiSettings.wake_word || '').toLowerCase().replace(/[^a-z0-9à-ú]/gi, '');
    const rawExec = (aiSettings.execute_word || '').toLowerCase().replace(/[^a-z0-9à-ú]/gi, '');
    const rawNewChat = (aiSettings.new_chat_word || '').toLowerCase().replace(/[^a-z0-9à-ú]/gi, '');

    const originalNormalized = trimmedText.toLowerCase().replace(/[^a-z0-9à-ú]/gi, '');
    let textToAnalyze = originalNormalized;
    
    // Se a pessoa disse "Cascata novo chat", removemos o "Cascata" para sobrar só "novochat"
    if (rawWake) {
        textToAnalyze = textToAnalyze.split(rawWake).join('');
    }

    // Se sobrou vazio (disse só Cascata), ou sobrou exatamente o exec, ou o newchat
    if (textToAnalyze === '' || textToAnalyze === rawExec || textToAnalyze === rawNewChat) {
        isSendingRef.current = false;
        setInput('');
        inputRef.current = '';
        transcriptBuffer.current = '';
        if (silenceTimer.current) {
            clearTimeout(silenceTimer.current);
            silenceTimer.current = null;
        }

        if (textToAnalyze === rawExec || originalNormalized === rawExec) {
            console.log(`%c[Cascata Architect] Safety Block: EXECUTE keyword intercepted`, 'color: #f59e0b; font-weight: bold;', { original: trimmedText });
            autoExecutePendingBlocks();
        } else if (textToAnalyze === rawNewChat || originalNormalized === rawNewChat) {
            console.log(`%c[Cascata Architect] Safety Block: NEW CHAT keyword intercepted`, 'color: #f59e0b; font-weight: bold;', { original: trimmedText });
            startNewSession();
            transitionToListeningCommand('');
        } else {
            console.log(`%c[Cascata Architect] Safety Block: WAKE WORD only — no API call`, 'color: #f59e0b; font-weight: bold;', { original: trimmedText });
            // Era só a wake word pura
            if (source === 'voice') {
                setVoiceStateSafe('IDLE_WAKEWORD');
                if (isActiveListeningMode) startIdleWakeWordListening();
            }
        }
        return;
    }
    // --------------------------------------
    
    // --- BIBLIOTECA DE COMANDOS LOCAIS (Voice/Text Navigation) ---
    // Permite comandos locais avançados sem chamar a IA, como navegar e abrir telas.
    let commandText = trimmedText.trim();
    console.log('[Cascata Architect] Capturing text:', commandText);

    const wakeStr = (aiSettings.wake_word || '').trim();
    if (wakeStr && commandText.toLowerCase().startsWith(wakeStr.toLowerCase())) {
        commandText = commandText.substring(wakeStr.length).trim();
    }
    commandText = commandText.replace(/^[,.!\s]+/, ''); // Limpa pontuação (ex: "Cascata, ver...")

    // 1. Tentar match de Tabela Direta
    const viewTableRegex = /^(?:eu\s+quero\s+|gostaria\s+de\s+)?(?:ver|abrir|abre|abra|mostrar|mostre|mostra|ir\s+para|vá\s+para)\s+(?:a\s+)?tabela\s+([a-zA-Z0-9_]+)/i;
    const viewTableMatch = commandText.match(viewTableRegex);
    
    // 2. Tentar match de Telas/Menus - RESTRITO: só captura se mencionar explicitamente "tela", "página", "menu", "seção" ou "aba"
    // Isso evita que comandos como "crie uma tabela para mim" sejam capturados erroneamente
    const viewScreenRegex = /^(?:eu\s+quero\s+|gostaria\s+de\s+)?(?:ver|abrir|abre|abra|mostrar|mostre|mostra|ir\s+para|vá\s+para)\s+(?:a\s+|o\s+|as\s+|os\s+)?(?:tela\s+de\s+|página\s+de\s+|menu\s+de\s+|seção\s+de\s+|aba\s+de\s+)([a-zA-Z0-9_ çãõáéíóúâêîôû]+)/i;
    const viewScreenMatch = commandText.match(viewScreenRegex);

    if (viewTableMatch && viewTableMatch[1]) {
        const tableName = viewTableMatch[1].toLowerCase();
        isSendingRef.current = false;
        setInput('');
        inputRef.current = '';
        transcriptBuffer.current = '';
        if (silenceTimer.current) clearTimeout(silenceTimer.current);

        console.log('[Cascata Architect] Match Nav Table:', tableName);

        // Navega e solicita seleção
        localStorage.setItem('cascata_pending_table_selection', tableName);
        window.location.hash = `#/project/${projectId}/database`;
        
        if (source === 'voice') {
            speakText(`Abrindo a tabela ${tableName}`);
            setVoiceStateSafe('IDLE_WAKEWORD');
            if (isActiveListeningMode) startIdleWakeWordListening();
        }
        return;
    } else if (viewScreenMatch && viewScreenMatch[1]) {
        const screenStr = viewScreenMatch[1].toLowerCase().trim();
        const navMap: Record<string, { route: string, label: string, isGlobal?: boolean }> = {
            'banco de dados': { route: 'database', label: 'Banco de Dados' },
            'tabelas': { route: 'database', label: 'Banco de Dados' },
            'dados': { route: 'database', label: 'Banco de Dados' },
            'database': { route: 'database', label: 'Banco de Dados' },
            'autenticação': { route: 'auth', label: 'Autenticação' },
            'autenticacao': { route: 'auth', label: 'Autenticação' },
            'usuários': { route: 'auth', label: 'Usuários' },
            'usuarios': { route: 'auth', label: 'Usuários' },
            'auth': { route: 'auth', label: 'Autenticação' },
            'regras': { route: 'rls', label: 'Regras de Segurança' },
            'segurança': { route: 'rls', label: 'Regras de Segurança' },
            'seguranca': { route: 'rls', label: 'Regras de Segurança' },
            'políticas': { route: 'rls', label: 'Regras de Segurança' },
            'politicas': { route: 'rls', label: 'Regras de Segurança' },
            'rls': { route: 'rls', label: 'Regras de Segurança' },
            'rpc': { route: 'rpc', label: 'Funções RPC' },
            'funções': { route: 'rpc', label: 'Funções RPC' },
            'funcoes': { route: 'rpc', label: 'Funções RPC' },
            'lógica': { route: 'rpc', label: 'Lógica' },
            'logica': { route: 'rpc', label: 'Lógica' },
            'storage': { route: 'storage', label: 'Storage' },
            'arquivos': { route: 'storage', label: 'Arquivos' },
            'armazenamento': { route: 'storage', label: 'Armazenamento' },
            'eventos': { route: 'events', label: 'Eventos' },
            'gatilhos': { route: 'events', label: 'Eventos' },
            'webhook': { route: 'events', label: 'Webhooks' },
            'webhooks': { route: 'events', label: 'Webhooks' },
            'push': { route: 'push', label: 'Notificações Push' },
            'notificações': { route: 'push', label: 'Notificações Push' },
            'notificacoes': { route: 'push', label: 'Notificações Push' },
            'backups': { route: 'backups', label: 'Backups' },
            'backup': { route: 'backups', label: 'Backups' },
            'documentação': { route: 'docs', label: 'Documentação API' },
            'documentacao': { route: 'docs', label: 'Documentação API' },
            'api': { route: 'docs', label: 'Documentação API' },
            'resumo': { route: 'overview', label: 'Resumo do Projeto' },
            'dashboard': { route: 'overview', label: 'Dashboard' },
            'painel': { route: 'overview', label: 'Painel' },
            'configurações': { route: 'settings', label: 'Configurações de Sistema', isGlobal: true },
            'configuracoes': { route: 'settings', label: 'Configurações de Sistema', isGlobal: true },
            'sistema': { route: 'settings', label: 'Sistema', isGlobal: true }
        };

        const target = navMap[screenStr];
        if (target) {
            isSendingRef.current = false;
            setInput('');
            inputRef.current = '';
            transcriptBuffer.current = '';
            if (silenceTimer.current) clearTimeout(silenceTimer.current);

            console.log('[Cascata Architect] Match Nav Screen:', screenStr, '->', target.route);

            if (target.isGlobal) {
                window.location.hash = `#/${target.route}`;
            } else {
                window.location.hash = `#/project/${projectId}/${target.route}`;
            }
            
            if (source === 'voice') {
                speakText(`Abrindo ${target.label}`);
                setVoiceStateSafe('IDLE_WAKEWORD');
                if (isActiveListeningMode) startIdleWakeWordListening();
            }
            return;
        }
    }
    // -------------------------------------------------------------

    isSendingRef.current = true;
    lastSentMessageRef.current = trimmedText;
    
    console.log('[Cascata Architect] Sending to API:', trimmedText);

    // Clear buffers immediately to prevent re-sending
    transcriptBuffer.current = '';
    if (silenceTimer.current) {
        clearTimeout(silenceTimer.current);
        silenceTimer.current = null;
    }

    let currentSessionId = sessionId;
    if (!currentSessionId) {
        currentSessionId = localStorage.getItem(`ai_session_${projectId}`) || '';
        if (!currentSessionId) {
            currentSessionId = getUUID();
            localStorage.setItem(`ai_session_${projectId}`, currentSessionId);
            setSessionId(currentSessionId);
        }
    }
    
    const newMsg = { role: 'user' as const, content: trimmedText };
    setMessages(prev => [...prev, newMsg]);
    setInput('');
    inputRef.current = '';
    setIsProcessing(true);
    setVoiceStateSafe('PROCESSING');
    const requestSeq = ++sendSequenceRef.current;
    // Limpar idle timer durante processamento (a IA está "pensando")
    if (conversationIdleTimer.current) clearTimeout(conversationIdleTimer.current);

    try {
      const token = localStorage.getItem('cascata_token');
      const useStreaming = !!aiSettings.enable_streaming;
      const apiMode = aiSettings.response_mode === 'realtime' ? 'realtime' : 'chat_completions';
      const payload = {
        session_id: currentSessionId,
        messages: buildContextMessages(messages, newMsg).map(m => ({ role: m.role, content: m.content })),
        config: {},
        source,
        stream: useStreaming,
        api_mode: apiMode
      };
      
      if (useStreaming) {
        const res = await fetch(`/api/data/${projectId}/ai/chat`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${token}`
          },
          body: JSON.stringify(payload)
        });

        if (!res.ok || !res.body) {
          throw new Error(`Streaming unavailable (HTTP ${res.status})`);
        }

        const decoder = new TextDecoder();
        const reader = res.body.getReader();
        let buffer = '';
        let accumulated = '';
        let streamDone = false;

        setMessages(prev => [...prev, { role: 'assistant', content: '', type: 'text' }]);

        while (!streamDone) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });

          let splitIdx = buffer.indexOf('\n\n');
          while (splitIdx !== -1) {
            const eventBlock = buffer.slice(0, splitIdx).trim();
            buffer = buffer.slice(splitIdx + 2);

            const dataLines = eventBlock
              .split('\n')
              .filter(line => line.startsWith('data: '))
              .map(line => line.slice(6));

            for (const raw of dataLines) {
              if (!raw) continue;
              try {
                const evt = JSON.parse(raw);
                if (evt.delta) {
                  accumulated += evt.delta;
                  setMessages(prev => {
                    const next = [...prev];
                    if (next.length > 0 && next[next.length - 1].role === 'assistant') {
                      next[next.length - 1] = { ...next[next.length - 1], content: accumulated, type: 'text' };
                    }
                    return next;
                  });
                }
                if (evt.done) {
                  streamDone = true;
                }
              } catch (_) {
                // ignore malformed chunks from providers
              }
            }
            splitIdx = buffer.indexOf('\n\n');
          }
        }

        const content = accumulated.trim();
        console.log(`%c[Cascata Architect] Stream Response Received`, 'color: #10b981; font-weight: bold;', { length: content.length, preview: content.substring(0, 120) });

        // --- BACKEND NAVIGATION FALLBACK (Streaming) ---
        let navigationHandled = false;
        if (handleBackendNavigationFallback(content)) {
            navigationHandled = true;
        }

        if (!navigationHandled && content) {
          let type: 'text' | 'sql' | 'json' = 'text';
          let actionData = null;
          if (content.includes('```sql')) type = 'sql';
          if (content.includes('"action": "create_table"')) {
            type = 'json';
            actionData = extractJSON(content);
          }
          setMessages(prev => {
            const next = [...prev];
            if (next.length > 0 && next[next.length - 1].role === 'assistant') {
              next[next.length - 1] = { ...next[next.length - 1], content, type, actionData };
            }
            return next;
          });
          speakText(content);
        } else {
          const fallbackMsg = "Desculpe, não consegui processar a resposta.";
          setMessages(prev => {
            const next = [...prev];
            if (next.length > 0 && next[next.length - 1].role === 'assistant') {
              next[next.length - 1] = { ...next[next.length - 1], content: fallbackMsg, type: 'text', actionData: null };
            } else {
              next.push({ role: 'assistant', content: fallbackMsg });
            }
            return next;
          });
          speakText(fallbackMsg);
        }
      } else {
        const res = await fetch(`/api/data/${projectId}/ai/chat`, {
          method: 'POST',
          headers: { 
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${token}` 
          },
          body: JSON.stringify(payload)
        });

        const data = await res.json();
        console.log(`%c[Cascata Architect] API Response Received`, 'color: #10b981; font-weight: bold;', { hasChoices: !!data.choices, navigationFallback: !!data.navigation_fallback });

        // --- BACKEND NAVIGATION FALLBACK (Non-Streaming) ---
        if (data.navigation_fallback) {
            const fb = data.navigation_fallback;
            console.log(`%c[Cascata Architect] Backend Fallback Navigation Triggered!`, 'color: #ef4444; font-weight: bold;', fb);
            if (fb.target_type === 'table' && fb.table_name) {
                localStorage.setItem('cascata_pending_table_selection', fb.table_name);
                window.location.hash = `#/project/${projectId}/database`;
            } else if (fb.target_route) {
                if (fb.is_global) {
                    window.location.hash = `#/${fb.target_route}`;
                } else {
                    window.location.hash = `#/project/${projectId}/${fb.target_route}`;
                }
            }
            const navMsg = fb.spoken_feedback || `Navegando para ${fb.label || 'a tela solicitada'}`;
            setMessages(prev => [...prev, { role: 'assistant', content: navMsg, type: 'text' }]);
            speakText(navMsg);
        } else if (data.choices && data.choices[0]) {
          const content = data.choices[0].message.content || ''; 
          
          let type: 'text' | 'sql' | 'json' = 'text';
          let actionData = null;

          if (content.includes('```sql')) type = 'sql';
          if (content.includes('"action": "create_table"')) {
              type = 'json';
              actionData = extractJSON(content);
          }
          
          setMessages(prev => [...prev, { role: 'assistant', content, type, actionData }]);
          speakText(content);
        } else {
          const fallbackMsg = "Desculpe, não consegui processar a resposta.";
          setMessages(prev => [...prev, { role: 'assistant', content: fallbackMsg }]);
          speakText(fallbackMsg);
        }
      }

    } catch (e) {
      console.error(`%c[Cascata Architect] API Error`, 'color: #ef4444; font-weight: bold;', e);
      const errorMsg = "Erro de conexão com o cérebro da IA.";
      setMessages(prev => [...prev, { role: 'assistant', content: errorMsg }]);
      speakText(errorMsg);
    } finally {
      setIsProcessing(false);
      if (sendSequenceRef.current === requestSeq) isSendingRef.current = false;
      
      if (source === 'voice') {
          transcriptBuffer.current = '';
          resetConversationIdleTimer();
          // Delay para o speechSynthesis ter tempo de atualizar o estado
          setTimeout(() => {
              // Usa isSpeakingRef (sempre atual) em vez de window.speechSynthesis (pode ser stale)
              if (!isSpeakingRef.current) {
                  setVoiceStateSafe('IDLE_WAKEWORD');
                  if (isActiveListeningMode) startIdleWakeWordListening();
              }
              // Se está falando, o utterance.onend vai cuidar do restart
          }, 150);
      } else {
          // Keyboard input: no TTS, reinicia imediatamente
          setVoiceStateSafe('IDLE_WAKEWORD');
          if (isActiveListeningMode) startIdleWakeWordListening();
      }
    }
  };

  // --- INTELLIGENT SQL PARSER & EXECUTOR ---

  const autoExecutePendingBlocks = async () => {
      const blocksToExecute: { type: string, content: string, actionData: any }[] = [];
      for (let i = messages.length - 1; i >= 0; i--) {
          const msg = messages[i];
          if (msg.role === 'user') break;
          if (msg.role === 'assistant') {
              if (msg.type === 'sql' && msg.content) {
                  blocksToExecute.unshift({ type: 'sql', content: msg.content, actionData: null });
              } else if (msg.type === 'json' && msg.actionData) {
                  blocksToExecute.unshift({ type: 'json', content: msg.content, actionData: msg.actionData });
              }
          }
      }

      if (blocksToExecute.length === 0) {
          speakText("Nenhuma ação pendente para executar.");
          setTimeout(() => {
              setVoiceStateSafe('IDLE_WAKEWORD');
              if (isActiveListeningMode) startIdleWakeWordListening();
          }, 3000);
          return;
      }

      speakText("Executando ações pendentes...");
      for (const block of blocksToExecute) {
          if (block.type === 'sql') await executeSQL(block.content, true);
          else if (block.type === 'json') await executeJSONAction(block.actionData, true);
      }
      
      setVoiceStateSafe('IDLE_WAKEWORD');
      if (isActiveListeningMode) startIdleWakeWordListening();
  };

  const extractSQLBlocks = (content: string): string[] => {
      const blocks: string[] = [];
      const regex = /```sql\s*\n?([\s\S]*?)```/gi;
      let match;
      while ((match = regex.exec(content)) !== null) {
          const block = match[1].trim();
          if (block) blocks.push(block);
      }
      return blocks;
  };

  const executeSQLBlocks = async (sqlBlocks: string[], originalContent: string, autoConfirm: boolean = false) => {
      if (sqlBlocks.length === 0) return;
      if (!autoConfirm && !confirm(`Executar ${sqlBlocks.length} bloco(s) SQL no banco de dados?`)) return;

      const token = localStorage.getItem('cascata_token');
      const results: { block: number; success: boolean; rows?: number; error?: string; duration?: number }[] = [];

      for (let i = 0; i < sqlBlocks.length; i++) {
          const block = sqlBlocks[i];
          try {
              const res = await fetch(`/api/data/${projectId}/query`, {
                  method: 'POST',
                  headers: {
                      'Content-Type': 'application/json',
                      'Authorization': `Bearer ${token}`
                  },
                  body: JSON.stringify({ sql: block })
              });
              const data = await res.json();
              if (!res.ok || data.error) {
                  results.push({ block: i + 1, success: false, error: data.error || `HTTP ${res.status}` });
                  break; // Stop on first error
              } else {
                  results.push({ block: i + 1, success: true, rows: Array.isArray(data.rows) ? data.rows.length : 0, duration: data.duration });
              }
          } catch (e: any) {
              results.push({ block: i + 1, success: false, error: e.message || 'Erro de conexão' });
              break;
          }
      }

      // Build feedback message
      const failed = results.find(r => !r.success);
      if (failed) {
          const errorMsg = `Erro no bloco ${failed.block}: ${failed.error}`;
          const failedSQL = sqlBlocks[failed.block - 1];
          // Show error in chat
          setMessages(prev => [...prev, {
              role: 'assistant',
              content: `⚠️ **Erro na execução SQL**\n\n${errorMsg}\n\n*Enviando erro para a IA corrigir automaticamente...*`,
              type: 'text'
          }]);
          // Auto-send error feedback to AI
          await sendErrorFeedbackToAI(errorMsg, failedSQL, originalContent);
      } else {
          const successMsg = results.map(r => `✓ Bloco ${r.block}: ${r.rows} linha(s) (${r.duration}ms)`).join('\n');
          setMessages(prev => [...prev, {
              role: 'assistant',
              content: `✅ **SQL Executado com sucesso**\n\n${successMsg}`,
              type: 'text'
          }]);
      }

      // Reativar microfone após execução SQL
      destroyRecognition();
      isConversationModeRef.current = false;
      setIsWakeWordDetected(false);
      setVoiceStateSafe('IDLE_WAKEWORD');
      if (isActiveListeningMode) startIdleWakeWordListening();
  };

  const sendErrorFeedbackToAI = async (error: string, failedSQL: string, originalContent: string) => {
      const feedbackMsg = `O seguinte SQL retornou erro ao ser executado no banco de dados:\n\n\`\`\`sql\n${failedSQL}\n\`\`\`\n\nErro do banco: ${error}\n\nPor favor, analise o erro, corrija o SQL e retorne apenas o código SQL corrigido.`;
      
      const newMsg = { role: 'user' as const, content: feedbackMsg };
      setMessages(prev => [...prev, { role: 'user', content: `[Feedback automático] ${feedbackMsg.substring(0, 100)}...` }]);
      setIsProcessing(true);

      try {
          const token = localStorage.getItem('cascata_token');
          const currentSessionId = sessionId || localStorage.getItem(`ai_session_${projectId}`);
          
          const res = await fetch(`/api/data/${projectId}/ai/chat`, {
              method: 'POST',
              headers: {
                  'Content-Type': 'application/json',
                  'Authorization': `Bearer ${token}`
              },
              body: JSON.stringify({
                  session_id: currentSessionId,
                  messages: messages.concat(newMsg).map(m => ({ role: m.role, content: m.content })),
                  config: {},
                  source: 'keyboard'
              })
          });

          const data = await res.json();
          if (data.choices && data.choices[0]) {
              const content = data.choices[0].message.content || '';
              let type: 'text' | 'sql' | 'json' = 'text';
              let actionData = null;
              if (content.includes('```sql')) type = 'sql';
              if (content.includes('"action": "create_table"')) {
                  type = 'json';
                  actionData = extractJSON(content);
              }
              setMessages(prev => [...prev, { role: 'assistant', content, type, actionData }]);
              speakText(content);
          }
      } catch (e) {
          const errorMsg = "Erro ao solicitar correção da IA.";
          setMessages(prev => [...prev, { role: 'assistant', content: errorMsg }]);
      } finally {
          setIsProcessing(false);
          // Reativar microfone após feedback de erro
          destroyRecognition();
          isConversationModeRef.current = false;
          setIsWakeWordDetected(false);
          setVoiceStateSafe('IDLE_WAKEWORD');
          if (isActiveListeningMode) startIdleWakeWordListening();
      }
  };

  const executeSQL = async (sql: string, autoConfirm: boolean = false) => {
      const blocks = extractSQLBlocks(sql);
      if (blocks.length === 0) {
          if (!autoConfirm) alert("Nenhum bloco SQL encontrado na mensagem.");
          return;
      }
      await executeSQLBlocks(blocks, sql, autoConfirm);
  };

  const executeJSONAction = async (data: any, autoConfirm: boolean = false) => {
      if (data.action === 'create_table') {
          if (!autoConfirm && !confirm(`Criar tabela ${data.name}?`)) return;
          try {
              const token = localStorage.getItem('cascata_token');
              await fetch(`/api/data/${projectId}/tables`, {
                  method: 'POST',
                  headers: { 
                      'Content-Type': 'application/json',
                      'Authorization': `Bearer ${token}`
                  },
                  body: JSON.stringify({
                      name: data.name,
                      description: data.description,
                      columns: data.columns.map((c: any) => ({
                          name: c.name,
                          type: c.type,
                          primaryKey: c.isPrimaryKey,
                          description: c.description
                      }))
                  })
              });
              if (!autoConfirm) alert(`Tabela ${data.name} criada com sucesso!`);
          } catch(e) {
              if (!autoConfirm) alert("Erro ao criar tabela.");
          }
      }

      // Reativar microfone após execução JSON
      destroyRecognition();
      isConversationModeRef.current = false;
      setIsWakeWordDetected(false);
      setVoiceStateSafe('IDLE_WAKEWORD');
      if (isActiveListeningMode) startIdleWakeWordListening();
  };

  const renderMarkdown = (text: string = '') => {
      const lines = text.split('\n');
      return lines.map((line, idx) => {
          let processed = line.split(/(\*\*.*?\*\*)/g).map((part, i) => {
              if (part.startsWith('**') && part.endsWith('**')) {
                  return <strong key={i}>{part.slice(2, -2)}</strong>;
              }
              return part.split(/(`.*?`)/g).map((subPart, j) => {
                  if (subPart.startsWith('`') && subPart.endsWith('`')) {
                      return <code key={`${i}-${j}`} className="bg-slate-200 px-1 py-0.5 rounded text-indigo-700 font-mono text-[10px]">{subPart.slice(1, -1)}</code>;
                  }
                  return subPart;
              });
          });

          if (line.trim().startsWith('- ')) {
              return <li key={idx} className="ml-4 list-disc marker:text-indigo-400">{processed}</li>;
          }
          if (line.trim() === '') return <br key={idx} />;
          
          return <p key={idx} className="mb-1">{processed}</p>;
      });
  };

  if (!isOpen) {
    return (
      <button 
        onClick={() => setIsOpen(true)}
        className={`fixed bottom-8 right-8 w-16 h-16 rounded-full shadow-2xl flex items-center justify-center hover:scale-110 transition-transform z-[100] group ${isWakeWordDetected ? 'bg-emerald-500 animate-pulse' : 'bg-slate-900 text-white'}`}
      >
        {isWakeWordDetected ? <Mic size={24} className="text-white"/> : <Sparkles size={24} className="group-hover:animate-spin" />}
      </button>
    );
  }

  return (
    <div 
      className="fixed bottom-8 right-8 bg-white rounded-[2rem] shadow-2xl border border-slate-200 flex flex-col z-[100] animate-in slide-in-from-bottom-10 fade-in duration-300 overflow-hidden font-sans"
      style={{ width: dimensions.width, height: dimensions.height }}
    >
      {/* Resize Handle */}
      <div 
        onMouseDown={(e) => { e.stopPropagation(); setIsResizing(true); }}
        className="absolute top-0 left-0 w-6 h-6 cursor-nwse-resize z-50 flex items-center justify-center group"
      >
          <div className="w-2 h-2 bg-slate-300 rounded-full group-hover:bg-indigo-500 transition-colors"></div>
      </div>

      {/* Header */}
      <div 
        className={`p-6 text-white flex justify-between items-center transition-colors ${isWakeWordDetected ? 'bg-emerald-600' : 'bg-slate-900'}`}
        onDoubleClick={() => setIsResizing(false)}
      >
        <div className="flex items-center gap-3 select-none">
          {viewMode === 'history' ? (
              <button onClick={() => { setViewMode('chat'); setInput(''); }} className="p-2 bg-white/20 rounded-xl hover:bg-white/30 transition-all">
                  <ChevronLeft size={20}/>
              </button>
          ) : (
              <div className="w-10 h-10 bg-white/20 rounded-xl flex items-center justify-center shadow-lg backdrop-blur-sm">
                {isWakeWordDetected ? <Volume2 size={20} className="animate-pulse"/> : <Sparkles size={20} />}
              </div>
          )}
          <div>
            <h3 className="font-black text-lg tracking-tight">{viewMode === 'history' ? 'Histórico' : 'Architect'}</h3>
            <p className="text-[10px] font-medium opacity-80 uppercase tracking-widest">{viewMode === 'history' ? 'Sessões Anteriores' : (isWakeWordDetected ? 'Listening...' : 'AI Context Aware')}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
            {viewMode === 'chat' && (
                <button 
                    onClick={() => { setViewMode('history'); loadSessions(); setInput(''); }} 
                    className="p-2 hover:bg-white/10 rounded-full transition-colors text-white/70 hover:text-white"
                    title="Histórico"
                >
                    <Clock size={18}/>
                </button>
            )}
            <button 
                onClick={() => {
                    const isAtDefault = dimensions.width === 400 && dimensions.height === 600;
                    if (isAtDefault) {
                        setDimensions({ width: 800, height: 900 }); // Expandir ao máximo
                    } else {
                        setDimensions({ width: 400, height: 600 }); // Voltar ao padrão
                    }
                }}
                className="p-2 hover:bg-white/10 rounded-full transition-colors text-white/50 hover:text-white"
                title={dimensions.width === 400 && dimensions.height === 600 ? "Expandir" : "Restaurar"}
            >
                <Maximize2 size={14}/>
            </button>
            {selectionMode && (
                <button 
                    onClick={deleteSelectedMessages}
                    className="p-2 hover:bg-red-500/20 rounded-full transition-colors text-red-400 hover:text-red-300"
                    title={`Excluir ${selectedIndices.size} mensagem(s)`}
                >
                    <X size={20}/>
                </button>
            )}
            {!selectionMode && (
                <button onClick={() => setIsOpen(false)} className="p-2 hover:bg-white/10 rounded-full transition-colors"><X size={20}/></button>
            )}
            {selectionMode && (
                <button 
                    onClick={exitSelectionMode}
                    className="p-2 hover:bg-white/10 rounded-full transition-colors text-white/70"
                    title="Cancelar seleção"
                >
                    <X size={20}/>
                </button>
            )}
        </div>
      </div>

      {/* Chat Area */}
      {viewMode === 'chat' ? (
          <div ref={scrollRef} className="flex-1 overflow-y-auto p-6 space-y-6 bg-slate-50">
            {messages.length === 0 && (
              <div className="text-center mt-20 opacity-50">
                <Sparkles size={48} className="mx-auto text-indigo-300 mb-4" />
                <p className="text-sm font-bold text-slate-400">Como posso ajudar a construir hoje?</p>
                <p className="text-[10px] text-slate-400 font-medium mt-2 max-w-[200px] mx-auto">
                    Suporte nativo a PostgREST: Pergunte sobre URLs e conexão segura.
                </p>
                {aiSettings.active_listening && <p className="text-[10px] text-emerald-500 font-bold mt-4 uppercase tracking-widest">Listening for "{aiSettings.wake_word}"</p>}
                <button onClick={() => setViewMode('history')} className="mt-6 text-xs text-indigo-500 hover:underline">Ver Histórico</button>
              </div>
            )}
            {messages.map((msg: any, idx: number) => (
              <div key={idx} 
                className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'} ${selectedIndices.has(idx) ? 'bg-indigo-100/50 rounded-2xl' : ''}`}
                onMouseDown={() => handleMessageMouseDown(idx)}
                onMouseUp={handleMessageMouseUp}
                onMouseLeave={handleMessageMouseUp}
                onClick={() => handleMessageClick(idx)}
                style={{ cursor: selectionMode ? 'pointer' : 'default' }}
              >
                {selectionMode && (
                  <div className="flex items-center mr-2">
                    <div className={`w-5 h-5 rounded border-2 flex items-center justify-center ${selectedIndices.has(idx) ? 'bg-indigo-600 border-indigo-600' : 'border-slate-300 bg-white'}`}>
                      {selectedIndices.has(idx) && <Check size={12} className="text-white" />}
                    </div>
                  </div>
                )}
                <div className={`max-w-[90%] rounded-2xl p-4 text-sm shadow-sm ${msg.role === 'user' ? 'bg-indigo-600 text-white' : 'bg-white border border-slate-200 text-slate-700'} ${selectedIndices.has(idx) ? 'ring-2 ring-indigo-400' : ''}`}>
                  {(!msg.type || msg.type === 'text') && (
                      <div className="whitespace-pre-wrap leading-relaxed architect-markdown-content">
                          {msg.role === 'assistant' ? <div dangerouslySetInnerHTML={{ __html: marked.parse(msg.content) as string }} /> : msg.content}
                      </div>
                  )}
                  
                  {msg.type === 'sql' && (
                    <div className="space-y-3">
                      <div className="flex items-center justify-between text-[10px] font-black uppercase tracking-widest text-slate-400">
                        <span className="flex items-center gap-2"><Terminal size={12}/> Sugestão SQL</span>
                        <button 
                            onClick={() => {
                                navigator.clipboard.writeText(msg.content.replace(/```(sql|json)?/g, '').replace(/```/g, ''));
                                alert("SQL Copiado!");
                            }}
                            className="hover:text-indigo-600 transition-colors"
                            title="Copiar SQL"
                        >
                            <Copy size={12} />
                        </button>
                      </div>
                      <pre className="bg-slate-900 text-emerald-400 p-3 rounded-xl overflow-x-auto font-mono text-xs">
                        {msg.content.replace(/```(sql|json)?/g, '').replace(/```/g, '')}
                      </pre>
                      <button 
                        onClick={() => executeSQL(msg.content)}
                        className="w-full py-2 bg-emerald-50 text-emerald-600 font-bold text-xs rounded-lg hover:bg-emerald-100 flex items-center justify-center gap-2 transition-colors border border-emerald-100"
                      >
                        <Play size={12}/> Executar Agora
                      </button>
                    </div>
                  )}

                  {msg.type === 'json' && msg.actionData && (
                      <div className="space-y-3">
                          <div className="flex items-center gap-2 text-[10px] font-black uppercase tracking-widest text-indigo-500">
                            <Database size={12}/> {msg.actionData.action.replace('_', ' ')}
                          </div>
                          <div className="bg-slate-50 border border-slate-200 rounded-xl p-3">
                              <h4 className="font-bold text-slate-900">{msg.actionData.name}</h4>
                              <p className="text-xs text-slate-500 mb-2">{msg.actionData.description}</p>
                              <div className="space-y-1">
                                  {msg.actionData.columns.map((c: any) => (
                                      <div key={c.name} className="flex justify-between text-xs bg-white p-2 rounded border border-slate-100">
                                          <span className="font-mono font-bold">{c.name}</span>
                                          <span className="text-slate-400">{c.type}</span>
                                      </div>
                                  ))}
                              </div>
                          </div>
                          <button 
                            onClick={() => executeJSONAction(msg.actionData)}
                            className="w-full py-2 bg-indigo-600 text-white font-bold text-xs rounded-lg hover:bg-indigo-700 flex items-center justify-center gap-2 transition-colors shadow-lg shadow-indigo-100"
                          >
                            <Check size={14}/> Approve & Build
                          </button>
                      </div>
                  )}
                </div>
              </div>
            ))}
            {isProcessing && (
              <div className="flex justify-start">
                <div className="bg-white border border-slate-200 rounded-2xl p-4 flex gap-2 items-center">
                  <div className="w-2 h-2 bg-indigo-500 rounded-full animate-bounce"></div>
                  <div className="w-2 h-2 bg-indigo-500 rounded-full animate-bounce delay-75"></div>
                  <div className="w-2 h-2 bg-indigo-500 rounded-full animate-bounce delay-150"></div>
                </div>
              </div>
            )}
          </div>
      ) : (
          // HISTORY VIEW
          <div className="flex-1 overflow-y-auto p-6 space-y-4 bg-slate-50">
              <div className="flex justify-between items-center mb-4">
                  <h4 className="text-xs font-black uppercase text-slate-400 tracking-widest">Sessões Recentes</h4>
                  <button onClick={startNewSession} className="text-[10px] font-bold bg-indigo-100 text-indigo-700 px-3 py-1.5 rounded-lg hover:bg-indigo-200 transition-colors flex items-center gap-2">
                      <Plus size={12}/> Nova Conversa
                  </button>
              </div>
              
              {sessions.map((s) => (
                  <div key={s.id} onClick={() => { setSessionId(s.id); setViewMode('chat'); loadHistory(s.id); }} className="bg-white p-4 rounded-xl border border-slate-200 hover:border-indigo-300 hover:shadow-md transition-all cursor-pointer group">
                      <div className="flex justify-between items-start">
                          <div className="flex-1">
                              {editingTitleId === s.id ? (
                                  <input 
                                    autoFocus
                                    value={tempTitle}
                                    onChange={(e) => setTempTitle(e.target.value)}
                                    onBlur={() => handleRenameSession(s.id)}
                                    onKeyDown={(e) => e.key === 'Enter' && handleRenameSession(s.id)}
                                    onClick={(e) => e.stopPropagation()}
                                    className="font-bold text-sm text-slate-900 w-full border-none outline-none bg-slate-50 rounded px-2 py-1"
                                  />
                              ) : (
                                  <h5 className="font-bold text-sm text-slate-700 group-hover:text-indigo-600 transition-colors">{s.title || 'Conversa sem título'}</h5>
                              )}
                              <span className="text-[10px] text-slate-400 font-medium mt-1 block">{new Date(s.updated_at).toLocaleString()}</span>
                          </div>
                          <button 
                            onClick={(e) => { e.stopPropagation(); setEditingTitleId(s.id); setTempTitle(s.title); }} 
                            className="p-2 text-slate-300 hover:text-indigo-600 hover:bg-slate-50 rounded-lg transition-all"
                          >
                              <Edit2 size={14} />
                          </button>
                          <button
                            onClick={(e) => { e.stopPropagation(); if (confirm('Excluir esta conversa permanentemente?')) deleteSession(s.id); }}
                            className="p-2 text-slate-300 hover:text-red-600 hover:bg-red-50 rounded-lg transition-all"
                            title="Excluir conversa"
                          >
                              <Trash2 size={14} />
                          </button>
                      </div>
                  </div>
              ))}
              
              {sessions.length === 0 && (
                  <div className="text-center py-10 text-slate-400 text-xs font-bold">
                      Nenhuma sessão encontrada.
                  </div>
              )}
          </div>
      )}

      {/* Input Area (Transforms into Search in History Mode) */}
      <div className="p-4 bg-white border-t border-slate-100">
        <div className="relative flex items-center gap-2">
          {viewMode === 'history' ? (
              // SEARCH MODE
              <>
                <Search size={18} className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-400" />
                <input 
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && performSearch()}
                    placeholder="Buscar em todas as conversas..." 
                    className="w-full border-none rounded-2xl py-4 pl-12 pr-4 text-sm font-medium outline-none bg-slate-50 focus:ring-2 focus:ring-indigo-500/20 transition-all"
                />
              </>
          ) : (
              // CHAT MODE
              <>
                <input 
                    value={input}
                    onChange={(e) => setInput(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && handleSend()}
                    placeholder={isWakeWordDetected ? "Ouvindo comando..." : "Digite ou fale..."} 
                    className={`w-full border-none rounded-2xl py-4 pl-6 pr-12 text-sm font-medium outline-none focus:ring-2 transition-all ${isWakeWordDetected ? 'bg-emerald-50 focus:ring-emerald-500/20' : 'bg-slate-50 focus:ring-indigo-500/20'}`}
                />
                <div className="absolute right-2 flex items-center gap-1">
                    {window.speechSynthesis.speaking && (
                        <button 
                        onClick={stopSpeaking}
                        className="p-2 rounded-xl bg-amber-500 text-white hover:bg-amber-600 transition-all shadow-lg shadow-amber-500/40"
                        title="Parar áudio"
                        >
                            <Square size={14} fill="currentColor" />
                        </button>
                    )}
                    <button 
                    onClick={toggleListening}
                    className={`p-2 rounded-xl transition-all ${isWakeWordDetected || isListening ? 'bg-rose-500 text-white animate-pulse shadow-lg shadow-rose-500/40' : 'text-slate-400 hover:text-indigo-600 hover:bg-slate-100'}`}
                    >
                    {(isListening || isWakeWordDetected) ? (
                        <span className="relative flex h-4 w-4">
                            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-rose-200 opacity-75"></span>
                            <span className="relative inline-flex rounded-full h-4 w-4 bg-white/20 items-center justify-center"><MicOff size={12}/></span>
                        </span>
                    ) : <Mic size={18}/>}
                    </button>
                    <button 
                    id="ai-architect-send"
                    onClick={(e) => handleSend(e)}
                    disabled={!input.trim() || isProcessing}
                    className="p-2 bg-indigo-600 text-white rounded-xl hover:bg-indigo-700 transition-all disabled:opacity-50"
                    >
                    <Send size={18} />
                    </button>
                </div>
              </>
          )}
        </div>
      </div>
      <style>{`
        .architect-markdown-content h1, .architect-markdown-content h2 { @apply font-black text-slate-900 mt-4 mb-2; }
        .architect-markdown-content h3 { @apply font-bold text-slate-800 mt-3 mb-1 uppercase text-[10px] tracking-widest; }
        .architect-markdown-content p { margin-bottom: 0.75rem; }
        .architect-markdown-content ul, .architect-markdown-content ol { margin-bottom: 1rem; padding-left: 1.25rem; }
        .architect-markdown-content li { margin-bottom: 0.25rem; }
        .architect-markdown-content table { width: 100%; border-collapse: collapse; margin: 1rem 0; border: 1px solid #e2e8f0; border-radius: 0.5rem; overflow: hidden; }
        .architect-markdown-content th { background: #f8fafc; padding: 0.5rem; text-align: left; font-size: 9px; font-weight: 900; }
        .architect-markdown-content td { padding: 0.5rem; border-bottom: 1px solid #f1f5f9; font-size: 10px; }
        .architect-markdown-content pre { background: #1e293b; color: #f1f5f9; padding: 1rem; rounded: 0.75rem; margin: 1rem 0; overflow-x: auto; font-family: monospace; }
        .architect-markdown-content code { background: #f1f5f9; padding: 0.1rem 0.3rem; rounded: 0.25rem; color: #4f46e5; }
        .architect-markdown-content pre code { background: transparent; padding: 0; color: inherit; }
      `}</style>
    </div>
  );
};

export default CascataArchitect;
