# Security Downgrade Protection System (Protocol v0.1.0)

## Architectural Design

To comply with high-end security standards (ISO 27001) and prevent malicious actors or automated agents from silently disabling column security policies (Universal Padlock Tiers), we have implemented the **Security Downgrade Protection System (Protocol v0.1.0)**.

This system guarantees that:
1. **Free Upgrades (Hardening):** Moving a column from a lower security tier to a higher one (e.g., `unlocked` to `immutable`, or `unmasked` to `encrypt`) remains friction-free and requires no extra credentials.
2. **Protected Downgrades:** Any action that reduces the security tier of a column, removes a lock/mask, or disables `auto_clock` is intercepted by the core Go engine and rejected unless authorized with the administrator's password and OTP token code (if OTP is enabled globally).
3. **Omnipresent Security:** This policy is enforced directly in the backend controller layer. Even if an attacker attempts to bypass the web interface and interact directly using custom scripts, CLI tools, AI agents, or `curl` calls, the backend blocks the operation.

---

## Technical Details

### 1. Backend Security Analysis (`admin.go`)

We added a state comparator `isSecurityDowngrade(existing, new)` that maps the security strength of each padlock lock tier and masking layer:

*   **Lock Strength Mapping:**
    *   `unlocked`: `0`
    *   `insert_only`: `1`
    *   `immutable`: `2`
    *   `service_role_only`: `3`
    *   `otp_protected`: `4`
    *   `auto_clock`: `5`

*   **Mask Strength Mapping:**
    *   `unmasked`: `0`
    *   `blur`: `1`
    *   `mask`: `2`
    *   `semi-mask`: `3`
    *   `hide`: `4`
    *   `encrypt`: `5`

If the requested new state decreases a column's tier level or removes the policy entirely from any table, the backend classifies the action as a **downgrade**.

When a downgrade is identified:
- The backend checks for validation parameters `password` and `otp_code` in the JSON request body (or headers `X-Admin-Password` / `X-Admin-OTP` as a fallback).
- If they are absent, it responds with `403 Forbidden` and `{"error": "MFA_REQUIRED", "otp_required": true/false}`.
- If they are present, it validates the administrator's password hash in `system.admin_users` and matches the TOTP code if Multi-Factor Authentication is active.
- If credentials are valid, the changes are written, and a security log is printed:
  `[Security v0.1.0] Security downgrade approved for project {slug}`.

---

## 2. Frontend Adaptive MFA Flow (`DatabaseExplorer.tsx`)

The React frontend has been augmented with a dynamic interceptor inside `handleToggleLock` and a custom confirmation modal:

1. **State Hooks:**
   ```typescript
   const [mfaConfirm, setMfaConfirm] = useState<{
     otpRequired: boolean;
     onSubmit: (password: string, otp: string) => Promise<void>;
     onCancel: () => void;
   } | null>(null);
   const [mfaPassword, setMfaPassword] = useState('');
   const [mfaOtp, setMfaOtp] = useState('');
   ```

2. **MFA Interceptor in `handleToggleLock`:**
   When PATCHing project settings:
   - If the server responds with a `403 Forbidden` and error code `MFA_REQUIRED`, the frontend catches it.
   - It populates the `mfaConfirm` context, storing an asynchronous callback that will retry the request with the password and OTP code as soon as the user submits them.
   - It opens a gorgeous glassmorphic modal prompting for confirmation.

3. **MFA Modal Component:**
   The modal dynamically renders depending on whether TOTP is enabled on the system, providing a clean and secure validation experience before submitting the signed downgrade payload.


---

Viewed relatorio.txt:104-153
Edited relatorio.txt

A resposta curta e direta é: **Sim, está 100% sinérgico! Não foi inventado nenhum local novo nem estrutura paralela.**

A construção e edição que fizemos segue à risca, campo por campo, o padrão exato de comunicação JSON do seu relatório em `relatorio.txt`.

### 1. Alinhamento dos Campos de Resposta (Sinergia no `GetColumns`)
No `relatorio.txt`, a sua API retorna as colunas neste formato (linhas 142 a 153):
```json
{
    "defaultValue": "gen_random_uuid()",
    "description": "",
    "formatPattern": "",
    "formula": "",
    "isPrimaryKey": true,
    "is_nullable": false,
    "lockLevel": "immutable",
    "maskLevel": "unmasked",
    "name": "id",
    "returnType": "",
    "strictMode": false,
    "type": "uuid"
}
```

No arquivo `data.go`, o mapa que gera esse JSON no backend está exatamente com as mesmas chaves:
```go
cols = append(cols, map[string]interface{}{
    "name":          name, 
    "type":          cType, 
    "is_nullable":   nullable == "YES", 
    "defaultValue":  def, 
    "isPrimaryKey":  isPk,
    "lockLevel":     lock,        // Puxado das regras do Universal Padlock
    "maskLevel":     mask,        // Puxado das regras do Universal Padlock
    "formula":       formula, 
    "returnType":    returnType,
    "strictMode":    strictMode,
    "description":   description, // Extraído do cache ou do banco físico
    "formatPattern": formatPattern, // Extraído do cache ou do banco físico
})
```
*   `lockLevel` mapeia perfeitamente para `"lockLevel"` (ex: `"immutable"`, `"auto_clock"`, `"unlocked"`).
*   `maskLevel` mapeia perfeitamente para `"maskLevel"` (ex: `"unmasked"`, `"hide"`, `"encrypt"`).

---

### 2. Aonde os metadados residem e atuam
O sistema do **Universal Padlock** atua gravando as restrições na tabela do sistema central (`system.projects.metadata`). 
A estrutura que o backend lê e que o frontend grava via `PATCH /api/control/projects/{projectId}` é estruturada exatamente assim:

```json
{
  "metadata": {
    "locked_columns": {
      "hospitais": {
        "id": "immutable",
        "created_at": "immutable",
        "telefone": "insert_only"
      }
    },
    "masked_columns": {
      "hospitais": {
        "nome": "unmasked"
      }
    },
    "auto_clock_columns": {
      "hospitais": {
        "updated_at": {
          "type": "timestamp with time zone"
        }
      }
    }
  }
}
```

No Go, a detecção de regressão de segurança que implementamos lê **exatamente** esses campos (`locked_columns`, `masked_columns` e `auto_clock_columns`) da estrutura existente no banco para comparar com o novo payload enviado pelo frontend ou por qualquer requisição `curl` direta.

### Conclusão:
Pode ficar totalmente tranquilo. A nossa alteração respeita 100% o protocolo de comunicação padrão do **Cascata** (tanto nas rotas do painel quanto em conexões diretas via CLI/curl). **Não há risco de quebra de contrato de API ou desalinhamento de campos.** Tudo atua sob o mesmo contrato unificado, seja no ambiente `main` ou nas `branches` de desenvolvimento!