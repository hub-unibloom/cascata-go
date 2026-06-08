# Cascata Crypto Engine (CCE) - Sovereign Security Node

## 1. Introduction
The **Cascata Crypto Engine (CCE)** is the cornerstone of the Cascata security architecture. It is a sovereign, self-contained service designed to handle sensitive cryptographic operations, key lifecycle management, and secure secret storage. By isolating these operations from the main application logic, CCE minimizes the attack surface and ensures that even a compromise of the application layer does not automatically lead to the compromise of sensitive data.

CCE is built with Go, emphasizing performance and safety, and follows principles inspired by the "Vault" architecture, including cryptographic sealing and memory-hard key derivation.

---

## 2. Security Architecture & Cryptography

### 2.1 Key Hierarchy
CCE implements a structured key hierarchy to protect data at rest and in transit:

| Key Level | Name | Description |
| :--- | :--- | :--- |
| **L0** | **Master Secret** | A high-entropy hex string (minimum 64 hex chars / 32 bytes) provided by the administrator. It is the "Root of Trust". |
| **L1** | **Key Encryption Key (KEK)** | Derived from the Master Secret via Argon2id. It encrypts the entire KeyStore database. |
| **L2** | **Data Encryption Keys (DEK)** | 256-bit random keys generated on-demand for specific application contexts (e.g., `user_data`, `audit_logs`). |
| **L3** | **Wrapped Secrets** | Named secrets (like API keys) encrypted by a specific DEK (usually named `secrets`) and stored within the KeyStore. |

### 2.2 Key Derivation (KEK)
The KEK derivation is the first line of defense against brute-force attacks on the Master Secret.
- **Algorithm**: Argon2id (Memory-hard derivation).
- **Parameters**: 256MB RAM, 3 Iterations, 4-way Parallelism.
- **Hardware Binding (Static Salt)**: The derivation uses a salt derived from the machine's identity (`/etc/machine-id` or hostname). This ensures that a Master Secret stolen from one server cannot be used directly to decrypt a KeyStore backup from another server.
- **Memory Security**: Immediately after derivation, the Master Secret buffer is zeroed out in memory to prevent leakage via memory dumps.

### 2.3 Data Encryption (AES-GCM)
All encryption at the DEK and KEK levels uses **AES-256-GCM** (Galois/Counter Mode).
- **Authenticated Encryption**: Provides both confidentiality and integrity.
- **Nonce Isolation**: A unique 12-byte random nonce is generated for every single encryption operation and prepended to the ciphertext.
- **Integrity Tag**: An authentication tag is appended to ensure that any tampering with the ciphertext is detected during decryption.

---

## 3. Operational States: Sealed vs. Unsealed

CCE operates in two distinct states to ensure security during boot and idle time:

### 3.1 Sealed Mode (Safe State)
In this state:
- The KeyStore is encrypted on disk.
- No keys are held in memory.
- The KEK is not available.
- All API requests (except health checks and unseal) return `503 Service Unavailable` with `engine_sealed`.
- **Trigger**: CCE boots into this state if the `CASCATA_MASTER_SECRET` is missing or if the provided secret fails to decrypt the KeyStore.

### 3.2 Unsealed Mode (Operational)
In this state:
- The KeyStore has been successfully decrypted and loaded into RAM.
- Cryptographic operations are active.
- **Trigger**: Successful execution of the `/v1/sys/unseal` API or a valid `CASCATA_MASTER_SECRET` env var at boot.

---

## 4. Key Management & Store Logic

### 4.1 KeyStore Persistence
The KeyStore is a JSON-based database containing all DEKs and encrypted secrets.
- **Atomic Operations**: When updating the store, CCE writes to a `.tmp` file and performs an atomic `rename` operation to prevent corruption in case of power failure or crashes.
- **Format**: The stored file is a single AES-GCM encrypted blob.

### 4.2 Key Rotation & Versioning
CCE supports **On-Demand Key Rotation**.
- Every key has a version number.
- When a key is rotated, a new 32-byte random key is generated and appended to that key's history in the store.
- **Backward Compatibility**: CCE can decrypt data encrypted with any previous version of a key, but always uses the **latest version** for new encryption requests.

### 4.3 Default Keys
Upon initial database creation, CCE automatically generates:
- `system`: Used for internal system-level encryption.
- `backup`: Reserved for backup-related operations.
- `secrets`: Used as the default DEK for the Secret Management API.

---

## 5. Security Features

### 5.1 The Tarpit Mechanism
To mitigate online brute-force and side-channel attacks, CCE includes a **Tarpit**:
- **Threshold**: Defaults to 50 requests per second (configurable).
- **Exponential Penalty**: If the threshold is exceeded, CCE injects a sleep delay: `delay = 2^(excess_requests) ms` (capped at 30 seconds).
- **Scope**: Applied to all decryption and unseal attempts.

### 5.2 Input Sanitization
- **Key/Secret Names**: Names are restricted to prevent injection attacks. Characters like `/`, `\`, `..`, `$`, `#` are forbidden.
- **Base64 Validation**: All plaintexts and ciphertexts are strictly validated for Base64 integrity.

---

## 6. API Documentation

### 6.1 Authentication
The header `X-Crypto-Auth` is **mandatory** for all non-health routes. Its value must match the `INTERNAL_CTRL_SECRET` environment variable.

### 6.2 Data Encryption & Decryption

#### `POST /v1/encrypt`
Encrypts a payload.
- **Request**: `{"key": "data_key", "plaintext": "<base64>"}`
- **Behavior**: If `data_key` does not exist, it is generated automatically.
- **Response**: `{"ciphertext": "cse:v1:data_key:1:<base64_payload>"}`

#### `POST /v1/decrypt`
Decrypts a CCE envelope.
- **Request**: `{"ciphertext": "cse:v1:data_key:1:<base64_payload>"}`
- **Behavior**: Extracts key name and version from the envelope to find the correct DEK.
- **Response**: `{"plaintext": "<base64>"}`

#### `POST /v1/encrypt-batch` / `POST /v1/decrypt-batch`
High-performance variants that process an array of items in a single request.

### 6.3 Secret Management

#### `POST /v1/secrets/store/:name`
Encrypts and stores a string as a named secret in the KeyStore.
- **Format**: The secret is encrypted using the internal `secrets` key.

#### `GET /v1/secrets/retrieve/:name`
Retrieves and decrypts a named secret.

### 6.4 System Administration

#### `POST /v1/sys/unseal`
Attempts to unseal the engine with the provided Master Secret.
- **Idempotency**: Returns success if already unsealed.

#### `POST /v1/sys/rekey`
Rotates the Master Secret.
- **Mechanism**: Decrypts the entire KeyStore with the old KEK and re-encrypts it with a new KEK derived from the new Master Secret.

#### `GET /v1/sys/fingerprint`
Returns a stable SHA-256 hash (truncated to 16 chars) of the KEK. Useful for verifying that two CCE instances are using the same Master Secret without exposing the secret itself.

---

## 7. Configuration Reference

| Environment Variable | Required | Default | Description |
| :--- | :--- | :--- | :--- |
| `INTERNAL_CTRL_SECRET` | **YES** | - | Authentication token for API clients. |
| `CASCATA_MASTER_SECRET` | No | - | Hex secret for auto-unseal at boot. |
| `CRYPTO_DB_PATH` | No | `/data/crypto/keys.enc` | Storage path for the encrypted KeyStore. |
| `PORT` | No | `3000` | Listening port. |
| `PORT` | No | `3000` | Listening port. |

---

## 8. Envelope Format (CSE v1)
CCE uses a colon-separated string format for all ciphertexts:
`cse:v1:<key_name>:<version>:<base64_payload>`

- `cse`: Identifier (Cascata Storage Envelope).
- `v1`: Version of the envelope format.
- `key_name`: The DEK name used.
- `version`: The version of the DEK.
- `base64_payload`: The AES-GCM result (nonce + ciphertext + tag).
---

## 9. Cascata Vault Integration (High-Level)

While the **Crypto Engine (CCE)** provides the low-level cryptographic primitives, the **Cascata Vault** is the orchestration layer that implements business logic, item types, and release policies.

### 9.1 Vault Item Types
Secrets in Cascata are classified into specific types to optimize their handling and UI presentation:

- **Folder**: Logical containers for organizing secrets.
- **Key**: General purpose cryptographic keys or passwords.
- **Cert**: SSL/TLS certificates or public/private key pairs.
- **Env**: Environment variables used in runtime configurations.
- **File**: Binary or text files (stored as Base64 in CCE).

### 9.2 Release Policies
Release policies determine **when** and **where** a secret can be decrypted:

| Policy | UI Visibility | Runtime Access | Usage |
| :--- | :---: | :---: | :--- |
| **Exportable** | YES | YES | Secrets that can be revealed/downloaded by admins. |
| **Runtime** | NO | YES | Default. Only accessible by automation/RPC/system. |
| **Verify Only** | NO | NO | Restricted to HMAC verification (webhooks). |
| **Sign Only** | NO | NO | Restricted to digital signing operations. |

### 9.3 Metadata & Records
Each secret record in the Cascata Vault contains the following standard fields:

- **Name**: Unique identifier within a project/folder.
- **Type**: One of the types mentioned in 9.1.
- **Description**: Human-readable context for the secret.
- **Content**: The actual sensitive value (stored as ciphertext in CCE).
- **Release Policy**: The constraints defined in 9.2.
- **Folder (Parent ID)**: The location in the vault hierarchy.

### 9.4 Access Control Flow
1. **Request**: A service (e.g., Nexus Automation) requests a secret.
2. **Policy Check**: The Vault Service verifies if the `Purpose` (e.g., `automation_runtime`) matches the `Release Policy`.
3. **Decryption**: If authorized, CCE is called via `/v1/decrypt` using the `X-Crypto-Auth` internal handshake.
4. **Delivery**: The plaintext is delivered to the runtime environment and cleared after use.
