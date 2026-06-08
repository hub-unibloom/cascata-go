# 🏰 Cascata Sovereign Auth: Manual Técnico Completo de Arquitetura e Integração

Este documento descreve detalhadamente a arquitetura de autenticação soberana e multi-tenant do Cascata. O sistema opera sob uma filosofia de isolamento físico e lógico (modelo condomínio), com execução nativa em Go e persistência direta no PostgreSQL. Este guia serve como referência técnica absoluta para engenheiros de software e agentes de IA desenvolverem integrações seguras de ponta a ponta.

---

## 1. Princípios Arquiteturais e Modelo Condomínio

O Cascata é uma plataforma Backend-as-a-Service (BaaS) multi-tenant projetada para rodar de forma escalável e segura. Em vez de centralizar todos os tenants em uma única tabela compartilhada, o sistema adota o **Isolamento por Banco de Dados (Physical & Logical Isolation)**:

1. **Isolamento Físico (Database Pool Routing):** Cada projeto (tenant) possui seu próprio pool de conexões PostgreSQL independente (`pgxpool.Pool`), apontando para um banco ou schema físico isolado.
2. **Contextualização de Conexões:** Toda requisição à API de dados ou autenticação resolve o tenant a partir do domínio customizado ou slug na URL. O middleware resolve e injeta o pool específico (`ProjectPool`) no contexto da requisição (`types.CascataRequest`).
3. **Row-Level Security (RLS) Nativo:** A autorização base é delegada diretamente ao PostgreSQL. O motor de execução ativa transações locais isoladas definindo a role do banco (`anon`, `authenticated`, `service_role`) e injetando as declarações do JWT (`sub`, `email`, `role`) no contexto da sessão da transação do banco.

---

## 2. Modelagem de Dados no Banco do Projeto

Cada banco de dados de projeto contém um schema interno chamado `auth` que gerencia o ciclo de vida das identidades, sessões e chaves de MFA.

```
                    ┌─────────────────────────┐
                    │       auth.users        │
                    ├─────────────────────────┤
                    │ id (UUID, PK)           │◄┐
                    │ raw_user_meta_data (JSON)│ │
                    │ user_concatenation (ENUM)│ │
                    │ created_at / updated_at │ │
                    │ banned (BOOL)           │ │
                    └─────────────────────────┘ │
                                 │              │
         ┌───────────────────────┴──────────────┼──────────────────────┐
         │ 1                                    │ 1                    │ 1
         ▼ N                                    ▼ N                    ▼ N
┌─────────────────────────┐    ┌─────────────────────────┐    ┌─────────────────────────┐
│     auth.identities     │    │   auth.refresh_tokens   │    │     auth.otp_codes      │
├─────────────────────────┤    ├─────────────────────────┤    ├─────────────────────────┤
│ id (UUID, PK)           │    │ id (UUID, PK)           │    │ provider (TEXT, PK)     │
│ user_id (UUID, FK) ────┼┘   │ user_id (UUID, FK) ────┼┘   │ identifier (TEXT, PK)   │
│ provider (TEXT)         │    │ token_hash (TEXT)       │    │ code (TEXT)             │
│ identifier (TEXT)       │    │ revoked (BOOL)          │    │ expires_at (TIMESTAMPTZ)│
│ password_hash (TEXT, Null)   │ ip_address (TEXT)       │    │ attempts (INT)          │
│ identity_data (JSONB)   │    │ user_agent (TEXT)       │    └─────────────────────────┘
│ verified_at (TIMESTAMPTZ)    │ fingerprint_hash (TEXT) │
└─────────────────────────┘    └─────────────────────────┘
```

### Tabelas do Schema `auth`

#### `auth.users`
Tabela central que identifica a entidade humana única. Não armazena segredos de autenticação diretamente, mas atua como âncora de relacionamentos.
* `id` (`UUID`, Primary Key): Identificador único global do usuário.
* `raw_user_meta_data` (`JSONB`): Metadados dinâmicos do perfil (ex: nome, avatar).
* `user_concatenation` (`public.user_concatenation[]`): Array de enums apontando para tabelas públicas vinculadas (Identity-Aware Linkage).
* `banned` (`BOOLEAN`): Flag administrativa para neutralização imediata.
* `created_at` / `updated_at`: Timestamps UTC.

#### `auth.identities`
Implementa o conceito de **User Identity Mesh**. Um usuário central pode possuir múltiplos canais de entrada.
* `id` (`UUID`, Primary Key): Identificador da identidade específica.
* `user_id` (`UUID`, Foreign Key referencing `auth.users(id)` ON DELETE CASCADE).
* `provider` (`TEXT`): A estratégia de autenticação (ex: `email`, `google`, `github`, `totp`, `biometria`).
* `identifier` (`TEXT`): O valor exclusivo do canal (ex: email do usuário, ID do provedor OAuth, segredo base32 do TOTP, chave pública FIDO2).
* `password_hash` (`TEXT`, Nullable): Hash bcrypt da senha de login (usado apenas se o provedor exigir senha, como no caso de `email`).
* `verified_at` (`TIMESTAMPTZ`, Nullable): Data de confirmação do canal.

#### `auth.refresh_tokens`
Armazena hashes criptográficos dos tokens de atualização de sessão para rotação de tokens.
* `id` (`UUID`, Primary Key).
* `token_hash` (`TEXT`): Hash SHA-256 do refresh token bruto de uso único.
* `user_id` (`UUID`, Foreign Key).
* `revoked` (`BOOLEAN`): Bloqueio lógico imediato do token.
* `ip_address` / `user_agent`: Dados para verificação de sequestro de sessão.
* `fingerprint_hash` (`TEXT`): Hash SHA-256 da composição de hardware/rede (Edge Lock).

#### `auth.otp_codes`
Centraliza códigos temporários para logins sem senha (Passwordless) e MFA agnóstico.
* `provider` (`TEXT`, Composite PK): Canal de disparo (ex: `email`, `sms`, `whatsapp`).
* `identifier` (`TEXT`, Composite PK): Destino do código (ex: `user@domain.com`, `+5511999999999`).
* `code` (`TEXT`): O valor numérico do OTP (gerado de forma randômica e segura).
* `expires_at` (`TIMESTAMPTZ`): Data limite de expiração (padrão: 15 minutos).
* `attempts` (`INTEGER`): Contador para prevenção de brute-force.

---

## 3. O Ecossistema "User Identity Mesh"

O modelo de autenticação do Cascata separa a identidade humana única dos canais de autenticação. 

```
                               ┌─────────────────┐
                               │   Human User    │
                               │  (auth.users)   │
                               └─────────┬────────┘
                                         │
             ┌───────────────────────────┼───────────────────────────┐
             │                           │                           │
             ▼                           ▼                           ▼
   ┌───────────────────┐       ┌───────────────────┐       ┌───────────────────┐
   │    Identity A     │       │    Identity B     │       │    Identity C     │
   │  provider: email  │       │ provider: google  │       │  provider: totp   │
   │  id: user@t.com   │       │ id: 10982376483   │       │ id: JBSWY3DPEHPK3 │
   └───────────────────┘       └───────────────────┘       └───────────────────┘
```

> [!NOTE]
> Essa descentralização permite a vinculação progressiva de identidades. Um usuário pode se registrar originalmente via email/senha, posteriormente vincular sua conta do Google e, finalmente, registrar o TOTP do Google Authenticator e uma chave biométrica (Passkey) — todos apontando para o mesmo registro em `auth.users`.

### Mecanismo de Vinculação Progressiva (Linkage API)
Para atrelar novas estratégias de autenticação a uma conta existente:
1. O usuário precisa estar autenticado com uma sessão ativa (`authenticated`).
2. Chama o endpoint administrativo `/auth/users/:id/identities` com os dados do novo provedor.
3. O sistema insere a nova tupla em `auth.identities` se a credencial não estiver associada a nenhum outro usuário.

---

## 4. Endpoints de Integração e Fluxos de Autenticação

Todos os endpoints operam sob o padrão de compatibilidade `/auth/v1/*` roteados dinamicamente com base no tenant ativo.

---

### Fluxo 1: Criação de Conta (Signup)

Cadastra um novo usuário no sistema e gera a primeira identidade no "Mesh".

```
  Client/App                                                  Cascata API
      │                                                            │
      ├─────── POST /auth/v1/signup ──────────────────────────────>│
      │        Body: email, password, profile metadata             │
      │                                                            │
      │                                             [Verifica se identidade existe]
      │                                             [Abre transação SQL]
      │                                             [Insere auth.users]
      │                                             [Gera Bcrypt Hash da senha]
      │                                             [Insere auth.identities]
      │                                             [Emite Session: Access + Refresh]
      │                                                            │
      │<────── HTTP 201 (Session JSON Response) ───────────────────┤
      │                                                            │
```

#### Request: `POST /auth/v1/signup`
* **Headers:**
  ```http
  Content-Type: application/json
  apikey: anon_key_do_projeto
  ```
* **Body:**
  ```json
  {
    "email": "dev@cascata.io",
    "password": "SuperSecurePassword123",
    "provider": "email",
    "data": {
      "display_name": "Senior Developer",
      "company": "Cascata Enterprises"
    }
  }
  ```

#### Response: `201 Created`
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsIn...",
  "token_type": "bearer",
  "expires_in": 3600,
  "expires_at": 1779278000,
  "refresh_token": "a1f9e2b83c...",
  "user": {
    "id": "e9a05f32-6cd2-4c28-98e3-0c1514bc912a",
    "aud": "authenticated",
    "role": "authenticated",
    "email": "dev@cascata.io",
    "email_confirmed_at": "2026-05-19T23:00:00Z",
    "user_metadata": {
      "display_name": "Senior Developer",
      "company": "Cascata Enterprises"
    },
    "identities": [
      {
        "id": "4b68e910-1cd2-4aef-b924-f3c1b82d3f44",
        "user_id": "e9a05f32-6cd2-4c28-98e3-0c1514bc912a",
        "provider": "email",
        "identity_data": {
          "email": "dev@cascata.io"
        },
        "verified_at": "2026-05-19T23:00:00Z"
      }
    ],
    "user_concatenation": ["vazio"]
  }
}
```

---

### Fluxo 2: Login Convencional e Interceptação de MFA (Step-Up Guard)

Quando o usuário tenta se autenticar com email e senha, o motor avalia se há um fator de MFA (como TOTP) registrado para o usuário. Em caso positivo, o token de sessão é retido e uma resposta especial de desafio de MFA é retornada.

```
Client/App                                                   Cascata API
    │                                                             │
    ├─────── POST /auth/v1/token (grant_type=password) ──────────>│
    │        Body: email, password                                │
    │                                                             │
    │                                            [Valida credencial no auth.identities]
    │                                            [MFA Check: Identidade TOTP ativa?]
    │                                            [SE SIM: Interrompe fluxo]
    │                                                             │
    │<────── HTTP 403 Forbidden (MFA Challenge Request) ──────────┤
    │        Body: {"error": "step_up_required", ...}             │
    │                                                             │
    │                                                             │
    │───( O App renderiza tela do autenticador e captura o OTP )──│
    │                                                             │
    │                                                             │
    ├─────── POST /auth/v1/token (grant_type=password) ──────────>│
    │        Body: email, password, totp_code                     │
    │                                                             │
    │                                            [Verifica credenciais]
    │                                            [Valida OTP do TOTP usando segredo]
    │                                            [Gera Session: Access + Refresh]
    │                                                             │
    │<────── HTTP 200 OK (Full Session Payload) ──────────────────┤
    │                                                             │
```

#### Request Inicial: `POST /auth/v1/token`
* **Body:**
  ```json
  {
    "grant_type": "password",
    "email": "admin@cascata.io",
    "password": "MySecretPassword"
  }
  ```

#### Response de Bloqueio (MFA Ativo): `403 Forbidden`
```json
{
  "error": "step_up_required",
  "message": "This account requires additional verification to sign in.",
  "supported_factors": ["totp"]
}
```

#### Request Final (Com Prova): `POST /auth/v1/token`
* **Body:**
  ```json
  {
    "grant_type": "password",
    "email": "admin@cascata.io",
    "password": "MySecretPassword",
    "totp_code": "847291"
  }
  ```

#### Response Final: `200 OK`
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "token_type": "bearer",
  "expires_in": 3600,
  "refresh_token": "c7a8b9f1d0..."
}
```

---

### Fluxo 3: Desafio OTP Agnóstico (Passwordless / Magic Link)

Permite autenticar usuários sem o uso de senhas estáticas. O Cascata gera um código OTP e o delega ao subsistema `AuthDispatchService`, que despacha via SMTP, Resend API, Webhooks customizados, ou fluxos automatizados do orquestrador do projeto (Nexus Automation).

```
Client/App                                                   Cascata API
    │                                                             │
    ├─────── POST /auth/v1/challenge ────────────────────────────>│
    │        Body: provider (whatsapp/email), identifier          │
    │                                                             │
    │                                             [Gera OTP seguro de 6 dígitos]
    │                                             [Registra em auth.otp_codes]
    │                                             [Chama Dispatcher de Notificações]
    │                                             [Envia via SMTP / Webhook / Nexus]
    │                                                             │
    │<────── HTTP 200 OK (Challenge Initiated) ───────────────────┤
    │                                                             │
    │                                                             │
    │─────────( Usuário recebe o código OTP em seu canal )────────│
    │                                                             │
    │                                                             │
    ├─────── POST /auth/v1/verify-challenge ─────────────────────>│
    │        Body: provider, identifier, code                     │
    │                                                             │
    │                                             [Lê código do banco]
    │                                             [Verifica expiração/tentativas]
    │                                             [Queima o código (Burn-on-Read)]
    │                                             [Gera Session: Access + Refresh]
    │                                                             │
    │<────── HTTP 200 OK (Full Session Payload) ──────────────────┤
    │                                                             │
```

#### Passo 1: Disparar o Código OTP
* **Request:** `POST /auth/v1/challenge`
  ```json
  {
    "provider": "whatsapp",
    "identifier": "+5511999999999"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "success": true,
    "message": "Challenge initiated. Check your identifier."
  }
  ```

#### Passo 2: Validar o Código Recebido
* **Request:** `POST /auth/v1/verify-challenge`
  ```json
  {
    "provider": "whatsapp",
    "identifier": "+5511999999999",
    "code": "582910"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "access_token": "eyJhbGciOiJIUzI1NiIs...",
    "refresh_token": "82aefb37c0...",
    "expires_in": 3600,
    "token_type": "bearer",
    "user": {
      "id": "c0fa79b8-d218-4b7b-839f-2da98d1c7d23",
      "email": "+5511999999999"
    }
  }
  ```

---

### Fluxo 4: Renovação de Sessão (Rotation & Session Hijack Defense)

Para mitigar roubo de sessão, o Cascata utiliza uma transação atômica em nível de banco de dados (`auth.refresh_session_v3`) e valida o *fingerprint* de hardware enviado na requisição contra o persistido no token original.

```
Client/App                                                   Cascata API
    │                                                             │
    ├─────── POST /auth/v1/token (grant_type=refresh_token) ─────>│
    │        Body: refresh_token                                  │
    │        Header: X-Device-Fingerprint                         │
    │                                                             │
    │                                        [Calcula hash SHA256 do token bruto]
    │                                        [Chama procedure atômica do Postgres]
    │                                        [Executa swap do token e gera novo RT]
    │                                        [Valida Fingerprint do cabeçalho]
    │                                        [SE IMPEDIDO (Hijack): Revoga sessão]
    │                                        [Gera novo Access Token]
    │                                                             │
    │<────── HTTP 200 OK (New Session & New RT) ──────────────────┤
    │                                                             │
```

#### Request: `POST /auth/v1/token?grant_type=refresh_token`
* **Headers:**
  ```http
  X-Device-Fingerprint: composite_hash_hardware_value
  ```
* **Body:**
  ```json
  {
    "refresh_token": "old_brute_refresh_token_string"
  }
  ```

#### Response: `200 OK`
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsIn...",
  "token_type": "bearer",
  "expires_in": 3600,
  "refresh_token": "new_brute_refresh_token_string"
}
```

> [!WARNING]
> Se a validação de fingerprint falhar (divergência física de dispositivo/IP entre rotações), o banco revoga a sessão imediatamente. Todas as tentativas subsequentes com aquele refresh token retornarão erro de violação de segurança.

---

### Fluxo 5: Ativação de MFA / TOTP (Google Authenticator)

Permite que o usuário final adicione o fator TOTP à sua malha de identidades.

#### Passo 1: Solicitar o Segredo TOTP (Enrollment)
* **Request:** `POST /auth/v1/mfa/enroll`
  * **Headers:** `Authorization: Bearer <access_token>`
* **Response:** `200 OK`
  ```json
  {
    "secret": "JBSWY3DPEHPK3PXP",
    "qr_code_url": "otpauth://totp/CascataApp:user-uuid?secret=JBSWY3DPEHPK3PXP&issuer=CascataApp"
  }
  ```

#### Passo 2: Validar e Confirmar Ativação
O aplicativo deve exibir o QR Code, solicitar que o usuário digite o código de 6 dígitos gerado e enviar a resposta de confirmação.
* **Request:** `POST /auth/v1/mfa/verify`
  * **Body:**
    ```json
    {
      "secret": "JBSWY3DPEHPK3PXP",
      "code": "382910"
    }
    ```
* **Response:** `200 OK`
  ```json
  {
    "success": true,
    "message": "MFA activated successfully"
  }
  ```

---

### Fluxo 6: Cadastro de Biometria (Passkey / WebAuthn)

O Cascata oferece suporte nativo ao padrão FIDO2/WebAuthn. O cadastro de biometria é realizado em duas etapas.

#### Passo 1: Início do Cadastro (Enroll Start)
O backend retorna as opções de criação de credencial que o navegador do cliente usará para interagir com o autenticador do dispositivo.
* **Request:** `POST /auth/v1/webauthn/enroll`
  * **Headers:** `Authorization: Bearer <access_token>`
* **Response:** `200 OK`
  ```json
  {
    "options": {
      "publicKey": {
        "challenge": "A7c8d9... (base64)",
        "rp": { "name": "Cascata Passkeys", "id": "localhost" },
        "user": { "id": "uuid_bytes", "name": "user@email.com", "displayName": "user@email.com" },
        "pubKeyCredParams": [ { "type": "public-key", "alg": -7 } ]
      }
    },
    "session": { "challenge": "raw_challenge_data..." }
  }
  ```

#### Passo 2: Finalização do Cadastro (Enroll Finish)
O cliente envia o resultado gerado pelo dispositivo biométrico. O backend valida a assinatura criptográfica e insere a credencial biométrica na tabela `auth.identities`.
* **Request:** `POST /auth/v1/webauthn/enroll/finish`
  * **Body:**
    ```json
    {
      "session": { "challenge": "raw_challenge_data..." },
      "credential": { "id": "cred_id", "rawId": "raw_id", "type": "public-key", "response": { "attestationObject": "...", "clientDataJSON": "..." } }
    }
    ```
* **Response:** `200 OK`
  ```json
  {
    "success": true,
    "message": "Biometria/Passkey enrolled successfully."
  }
  ```

---

## 5. Universal Padlock: Segurança a Nível Transacional (Database Triggers)

O **Universal Padlock** é o sistema de blindagem mais avançado do Cascata. Em vez de delegar as regras de acesso a colunas críticas exclusivamente à camada de software (onde falhas de programação podem expor brechas), a blindagem é aplicada **dentro do motor de banco de dados do projeto**, executando em nível transacional atômico via triggers PL/pgSQL.

### A Anatomia da Defesa

```
[ HTTP PATCH Request ] ────> [ Auth Middleware ] ────> [ Data Controller ] 
                                  │                         │
                     (Verifica cabeçalho de Step-Up)         (Abre Transação PG)
                     - X-Cascata-Stepup-Provider            - SET LOCAL claim.sub
                     - X-Cascata-Stepup-Code                - SET LOCAL stepup.verified_providers
                                  │                         │
                                  ▼                         ▼
                     [ Valida OTP no Middleware ]       [ Executa Query UPDATE ]
                                  │                         │
                                  ▼                         ▼
                     [ Injeta verificação no ]          [ PG Trigger: enforce_dynamic_locks() ]
                     [ contexto do request:  ]              │
                     [ StepUpProviders = totp]              ├─► Verifica lock na coluna modificada
                                                            ├─► Valida se provider está no anel permitido
                                                            │   (ex: metadata->'allowed_factors')
                                                            │
                                                            ▼
                                                        [ SUCESSO: Commit ] ou [ REJEIÇÃO: Rollback ]
```

### Funcionamento Interno da Trigger `system.enforce_dynamic_locks()`

Quando uma coluna protegida por lock é modificada, a trigger entra em ação. Ela compara o estado da transação local com os fatores permitidos configurados no metadata do projeto.

Se uma coluna for marcada com o tipo de lock `otp_protected`, a trigger avalia a variável de sessão local `request.stepup.verified_providers`. Se a transação não possuir a prova de autenticação do fator configurado, a alteração é abortada com o erro `PDC01`.

#### Código da Trigger SQL:
```sql
CREATE OR REPLACE FUNCTION system.enforce_dynamic_locks()
RETURNS TRIGGER AS $$
DECLARE
    _project_slug TEXT;
    _is_otp_verified TEXT;
    _request_role TEXT;
    _lock_record RECORD;
    _old_value JSONB;
    _new_value JSONB;
BEGIN
    _project_slug := current_setting('request.jwt.claim.project_slug', true);
    IF _project_slug IS NULL THEN
        _project_slug := current_setting('app.current_project_slug', true);
    END IF;

    IF _project_slug IS NOT NULL THEN
        _is_otp_verified := current_setting('request.jwt.claim.otp_verified', true);
        _request_role := current_setting('request.jwt.claim.role', true);
        
        IF TG_OP = 'UPDATE' THEN
            _old_value := to_jsonb(OLD);
            _new_value := to_jsonb(NEW);
        ELSIF TG_OP = 'INSERT' THEN
            _new_value := to_jsonb(NEW);
            _old_value := '{}'::jsonb;
        END IF;

        FOR _lock_record IN 
            SELECT column_name, lock_type, metadata 
            FROM system.dynamic_security_locks 
            WHERE project_slug = _project_slug AND table_name = TG_TABLE_NAME
        LOOP
            -- Bloqueia alterações em colunas protegidas
            IF _new_value ? _lock_record.column_name AND (_old_value ->> _lock_record.column_name IS DISTINCT FROM _new_value ->> _lock_record.column_name) THEN
                
                -- Nível 1: Imutabilidade estrita
                IF _lock_record.lock_type IN ('insert_only', 'immutable') AND TG_OP = 'UPDATE' THEN
                    RAISE EXCEPTION USING ERRCODE = 'PDC02', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" is locked (' || _lock_record.lock_type || ') and cannot be updated.';
                END IF;
                
                -- Nível 2: Restrição de Role Administrativa
                IF _lock_record.lock_type = 'service_role_only' AND coalesce(_request_role, 'service_role') IN ('anon', 'authenticated') THEN
                    RAISE EXCEPTION USING ERRCODE = 'PDC04', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" requires SERVICE_ROLE system privileges to mutate.';
                END IF;

                -- Nível 3: Universal Padlock (Step-Up OTP Dinâmico)
                IF _lock_record.lock_type = 'otp_protected' THEN
                    _is_otp_verified := coalesce(current_setting('request.jwt.claim.otp_verified', true), 'false');
                    
                    IF _is_otp_verified != 'true' THEN
                        DECLARE
                            _stepup_providers TEXT := current_setting('request.stepup.verified_providers', true);
                            _allowed_providers JSONB := coalesce(_lock_record.metadata->'allowed_factors', '["totp", "email_otp", "sms_otp", "biometria"]'::jsonb);
                        BEGIN
                            -- Valida se o fator verificado na requisição está no anel permitido da coluna
                            IF _stepup_providers IS NULL OR _stepup_providers = '' OR NOT (_allowed_providers ? _stepup_providers) THEN
                                RAISE EXCEPTION USING ERRCODE = 'PDC01', MESSAGE = '{"error": "step_up_required", "message": "Security Lock Violation: Valid Step-Up Authorization Ring is required to mutate column \"' || _lock_record.column_name || '\".", "required_factors": ' || _allowed_providers::text || '}';
                            END IF;
                        END;
                    END IF;
                END IF;
            END IF;
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;
```

---

### Exemplo Prático de Bloqueio e Bypass

#### Tabela `public.contas_bancarias` com coluna `chave_pix` configurada como `otp_protected`:
```json
{
  "project_slug": "banco-soberano",
  "table_name": "contas_bancarias",
  "column_name": "chave_pix",
  "lock_type": "otp_protected",
  "metadata": {
    "allowed_factors": ["totp", "biometria"]
  }
}
```

#### Tentativa 1: Enviar UPDATE convencional (Sem cabeçalhos de Step-Up)
* **Request:** `PATCH /tables/contas_bancarias`
  * **Headers:** `Authorization: Bearer <user_jwt>`
  * **Body:**
    ```json
    {
      "chave_pix": "hacker@pix.com"
    }
    ```
* **Response:** `400 Bad Request`
  ```json
  {
    "error": "Security Lock Violation: Valid Step-Up Authorization Ring is required to mutate column \"chave_pix\".",
    "required_factors": ["totp", "biometria"]
  }
  ```

#### Tentativa 2: Enviar UPDATE com cabeçalhos de prova (Passando no Padlock)
O aplicativo obtém o código TOTP digitado pelo usuário e anexa nos cabeçalhos da requisição que atualiza o dado.
* **Request:** `PATCH /tables/contas_bancarias`
  * **Headers:**
    ```http
    Authorization: Bearer <user_jwt>
    X-Cascata-Stepup-Provider: totp
    X-Cascata-Stepup-Code: 481928
    ```
  * **Body:**
    ```json
    {
      "chave_pix": "new-val@pix.com"
    }
    ```
* **Response:** `200 OK`
  ```json
  {
    "success": true,
    "message": "Rows updated successfully."
  }
  ```

---

## 6. Configuração Multi-App (App Clients) e Redirecionamento

O Cascata gerencia a autenticação multi-origem em um mesmo projeto usando o conceito de **App Clients**. Cada App Client representa uma interface do projeto (ex: App Mobile, Landing Page, Painel do Cliente) e define suas próprias permissões e regras de isolamento.

```
                             ┌───────────────────┐
                             │   Project Slug    │
                             │ (banco-soberano)  │
                             └─────────┬─────────┘
                                       │
             ┌─────────────────────────┴─────────────────────────┐
             ▼                                                   ▼
   ┌───────────────────┐                               ┌───────────────────┐
   │   App Client A    │                               │   App Client B    │
   │   id: client-web  │                               │  id: client-mobi  │
   │   CORS: web.t.com │                               │  CORS: localhost  │
   └───────────────────┘                               └───────────────────┘
```

### Configurações por App Client
* `site_url`: O endereço base do aplicativo (utilizado como destino padrão após login OAuth).
* `allowed_origins` (CORS): Lista de origens permitidas. O sistema bloqueia redirects se a URI solicitada no parâmetro `redirect_to` não for correspondente.
* `anon_key`: Uma chave API pública vinculada diretamente a este cliente para Identity-Aware Key Bridging.

### Redirecionamento Seguro com Provedores OAuth
No fluxo de login via Google/GitHub, o parâmetro `redirect_to` e `app_client_id` (ou cabeçalho `apikey`) são criptografados no estado (`state`) do OAuth. Ao retornar do provedor, o Cascata descriptografa o estado, verifica a validade do redirect contra os `allowed_origins` do respectivo App Client e envia os tokens de autenticação via Hash Fragment na URL de destino do usuário.

---

## 7. Casos de Uso Reais e Tutoriais de Integração

---

### Caso de Uso 1: Aplicativo Mobile com Sessões Longas de 30 Dias

**Contexto:** Um aplicativo mobile precisa manter o usuário autenticado sem solicitar nova senha por 30 dias. O aplicativo autentica usando email e senha, obtendo tokens duradouros.

#### Passo 1: Login Inicial
O App envia credenciais via JSON ou form-urlencoded.
```http
POST /auth/v1/token HTTP/1.1
Host: api.cascata.io
Content-Type: application/json

{
  "grant_type": "password",
  "email": "mobile.user@example.com",
  "password": "MinhaSenhaSuperSegura"
}
```
**Resposta do Servidor (Session JSON):**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsIn...",
  "refresh_token": "6d92aef39c1b82d3f44aef2910ab3...",
  "expires_in": 3600,
  "token_type": "bearer"
}
```
*O aplicativo armazena de forma segura no Keychain/Keystore o `refresh_token` e temporariamente em memória o `access_token`.*

#### Passo 2: Renovação Automática da Sessão
Antes de expirar o `access_token` (geralmente a cada 1 hora), o app dispara em background:
```http
POST /auth/v1/token?grant_type=refresh_token HTTP/1.1
Host: api.cascata.io
Content-Type: application/json
X-Device-Fingerprint: f6a8d29bb183c0ff5213a8ef

{
  "refresh_token": "6d92aef39c1b82d3f44aef2910ab3..."
}
```
**Resposta do Servidor (Novos Tokens):**
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsIn...",
  "refresh_token": "82ab38f4d90ce123f4b82d921ab9...",
  "expires_in": 3600
}
```
*O `refresh_token` antigo é invalidado no banco do tenant no mesmo instante, prevenindo Replay Attacks.*

---

### Caso de Uso 2: Alteração de Dados Críticos Protegidos com TOTP (Step-Up)

**Contexto:** Uma Fintech utiliza o Cascata. A tabela `transacoes` possui a coluna `valor` protegida por lock de MFA do tipo `otp_protected` exigindo TOTP. O usuário autenticado tenta realizar uma transferência.

#### Passo 1: Tentativa sem Prova (Bloqueada)
O cliente envia o comando de escrita:
```http
POST /tables/transacoes/rows HTTP/1.1
Host: api.cascata.io
Authorization: Bearer eyJhbGciOiJIUzI1NiIsIn...
Content-Type: application/json

{
  "data": {
    "conta_destino": "12345-6",
    "valor": 15000.00
  }
}
```
**Resposta:** `400 Bad Request`
```json
{
  "error": "Security Lock Violation: Valid Step-Up Authorization Ring is required to mutate column \"valor\".",
  "required_factors": ["totp"]
}
```

#### Passo 2: O App solicita o Token TOTP do usuário
O app renderiza uma tela nativa solicitando o código do autenticador. O usuário fornece `298104`.

#### Passo 3: Enviar a escrita com Bypass
O app anexa os cabeçalhos de Step-Up na requisição original:
```http
POST /tables/transacoes/rows HTTP/1.1
Host: api.cascata.io
Authorization: Bearer eyJhbGciOiJIUzI1NiIsIn...
X-Cascata-Stepup-Provider: totp
X-Cascata-Stepup-Code: 298104
Content-Type: application/json

{
  "data": {
    "conta_destino": "12345-6",
    "valor": 15000.00
  }
}
```
**Resposta:** `201 Created`
```json
[
  {
    "id": "e9a05f32-6cd2-4c28-98e3-0c1514bc912a",
    "conta_destino": "12345-6",
    "valor": 15000.00,
    "created_at": "2026-05-19T23:45:00Z"
  }
]
```
*A trigger `system.enforce_dynamic_locks` foi avaliada no banco de dados, encontrando a variável local `request.stepup.verified_providers` definida como `'totp'`, e liberou a transação.*

---

### Caso de Uso 3: Login via Google OAuth em SPA React com Domínio Customizado

**Contexto:** Uma aplicação Web (React) rodando em `https://app.minhafintech.com.br` precisa fazer login via Google utilizando o domínio customizado de API do Cascata `https://api.minhafintech.com.br`.

#### Passo 1: O React inicia o fluxo redirecionando o navegador do usuário
O link de login chama:
```
https://api.minhafintech.com.br/auth/v1/authorize?provider=google&redirect_to=https://app.minhafintech.com.br/dashboard&app_client_id=client-web-prod
```
*O Cascata monta o estado seguro contendo a rota final e redireciona o usuário para o consentimento do Google.*

#### Passo 2: O usuário aceita a autorização no Google
O Google envia o usuário de volta com o código de autorização temporário para o Callback do Cascata:
```
GET https://api.minhafintech.com.br/auth/v1/callback?code=4/0AdQt8qiL...&state=eyJyZWRpcmVjdF90byI6Imh0dHBzOi...
```

#### Passo 3: O Cascata valida o código e envia os tokens
1. O backend do Cascata faz o intercâmbio do `code` com as APIs do Google e recupera o perfil do usuário.
2. Atualiza ou insere o usuário e cria a sessão.
3. Localiza o App Client `client-web-prod` a partir do `state`, verifica se `https://app.minhafintech.com.br` está no CORS permitido.
4. Redireciona o navegador do usuário para a URL de destino com o fragmento hash dos tokens:
```
https://app.minhafintech.com.br/dashboard#access_token=eyJhbGciOi...&refresh_token=92aef3c0...&expires_in=3600&token_type=bearer
```

#### Passo 4: O React recupera os tokens
No componente React `Dashboard`:
```javascript
useEffect(() => {
  const hash = window.location.hash;
  if (hash) {
    const params = new URLSearchParams(hash.replace('#', '?'));
    const accessToken = params.get('access_token');
    const refreshToken = params.get('refresh_token');
    
    if (accessToken && refreshToken) {
      localStorage.setItem('access_token', accessToken);
      localStorage.setItem('refresh_token', refreshToken);
      // Limpa a URL
      window.location.hash = '';
    }
  }
}, []);
```

---

### Caso de Uso 4: Autenticação Passwordless via WhatsApp/SMS Integrada ao Nexus

**Contexto:** Um portal de investimentos de alta segurança envia códigos de acesso direto para o WhatsApp do investidor usando automações internas do Cascata (Nexus Engine).

#### Passo 1: Solicitar o Desafio
O cliente informa o telefone celular:
```http
POST /auth/v1/challenge HTTP/1.1
Host: api.cascata.io
Content-Type: application/json

{
  "provider": "whatsapp",
  "identifier": "+5511988887777"
}
```

#### Passo 2: O Pipeline de Automação Interna (Nexus)
O Cascata registra o código na tabela `auth.otp_codes`. O Nexus captura o gatilho de inserção e dispara a automação associada:
* **Gatilho:** Inserção em `auth.otp_codes` onde `provider = 'whatsapp'`.
* **Ação executada:** Envio de payload via API externa do WhatsApp Business com o template aprovado contendo o código gerado.

#### Passo 3: O Usuário digita o código no Web
O cliente insere o código `581290` recebido no WhatsApp:
```http
POST /auth/v1/verify-challenge HTTP/1.1
Host: api.cascata.io
Content-Type: application/json

{
  "provider": "whatsapp",
  "identifier": "+5511988887777",
  "code": "581290"
}
```
*O sistema deleta o código do banco por segurança e devolve o JWT de sessão do investidor.*

---

## 8. Diagnóstico de Erros e Códigos de Status

Tabela de referência para diagnóstico e resolução de erros do motor de autenticação:

| Código HTTP | Código Interno (PostgreSQL/Go) | Descrição do Erro | Ação Corretiva Recomendada |
| :--- | :--- | :--- | :--- |
| `401 Unauthorized` | - | Token ausente, inválido ou expirado. | Enviar o token no cabeçalho `Authorization: Bearer <token>` ou renovar a sessão usando o `refresh_token`. |
| `401 Unauthorized` | `Identity Neutralized` | O usuário foi banido ou excluído pelo painel administrativo (Sovereign Panic Signal). | Bloquear imediatamente a sessão no cliente e alertar o suporte técnico. |
| `403 Forbidden` | `step_up_required` | O login inicial requer validação de MFA adicional. | Renderizar tela de MFA e enviar o código obtido para `/auth/v1/token` informando `totp_code`. |
| `400 Bad Request` | `PDC01` | Tentativa de atualizar ou inserir dados em coluna protegida sem Step-Up OTP ativo. | Solicitar autenticação complementar do usuário e enviar a credencial nos cabeçalhos `X-Cascata-Stepup-*`. |
| `400 Bad Request` | `PDC02` | Tentativa de atualizar uma coluna marcada como `immutable` ou `insert_only`. | Remover a coluna informada do corpo da requisição de atualização. |
| `403 Forbidden` | `PDC04` | Modificação de coluna restrita à role de serviço do sistema (`service_role`). | Impedir a chamada do cliente direto; executar a ação apenas por meio de chaves de API secretas administrativas. |
| `400 Bad Request` | `user_already_exists` | E-mail ou telefone de cadastro já associado a um usuário. | Solicitar recuperação de senha ou direcionar o usuário para a tela de login. |
| `400 Bad Request` | `Invalid login credentials` | Senha ou e-mail incorretos na validação convencional. | Exibir mensagem genérica de erro de credenciais. |
