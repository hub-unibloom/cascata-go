# Security Hardening Guide

## Critical: PostgreSQL Port Exposure

### Vulnerability
The `docker-compose.yml` exposes PostgreSQL on port 5432:
```yaml
db:
  ports:
    - "5432:5432"  # ❌ REMOVE THIS IN PRODUCTION
```

### Attack Vector
An attacker with host access can bypass ALL application security:
```bash
psql -h localhost -p 5432 -U cascata_admin -d cascata_system
```

### Solution 1: Remove Port Binding (Recommended)
```yaml
db:
  # Remove ports section entirely
  # PostgreSQL only accessible via cascata_data network
  networks:
    - cascata_data  # internal: true
```

### Solution 2: Firewall + pg_hba.conf
If port exposure is absolutely required:

**1. iptables/ufw on host:**
```bash
# Block external access to 5432
ufw deny 5432
ufw allow from 127.0.0.1 to any port 5432
```

**2. PostgreSQL pg_hba.conf:**
```conf
# Only accept connections from Docker network
host  all  all  172.18.0.0/16  scram-sha-256
host  all  all  127.0.0.1/32   scram-sha-256
# Reject everything else
host  all  all  0.0.0.0/0      reject
```

### Solution 3: Strong Credentials + Rotation
Even with port exposure, use:
- 32+ character random passwords
- Regular credential rotation
- `pgbouncer` auth_query for dynamic credentials

## Defense in Depth Strategy

### Layer 1: Network (Physical/Segmentation)
- PostgreSQL on internal-only Docker network
- No port bindings to host
- VPN-required for admin access

### Layer 2: Authentication (JWT + mTLS)
- Cascata validates JWT before ANY pool creation
- Consider mTLS between services
- Certificate pinning for internal services

### Layer 3: Authorization (RBAC + RLS)
- Every query runs with SET ROLE
- RLS policies enforce tenant isolation
- Service accounts with minimal privileges

### Layer 4: Monitoring (Audit + Alert)
- Log ALL database connections
- Alert on direct DB connections bypassing pgbouncer
- SIEM integration for anomaly detection

## Validation Script

Check if your deployment is vulnerable:
```bash
#!/bin/bash
# check_security.sh

echo "=== Checking PostgreSQL Exposure ==="

# Check if 5432 is listening on all interfaces
if netstat -tlnp 2>/dev/null | grep -q ":5432 " || \
   ss -tlnp 2>/dev/null | grep -q ":5432 " ; then
  echo "❌ CRITICAL: PostgreSQL is listening on host network"
  echo "   Run: netstat -tlnp | grep 5432"
  exit 1
else
  echo "✅ PostgreSQL not exposed on host network"
fi

# Check Docker network isolation
if docker network inspect cascata_data 2>/dev/null | grep -q '"internal": true'; then
  echo "✅ cascata_data network is internal"
else
  echo "❌ cascata_data network is not internal!"
  exit 1
fi

echo "=== Security Check Passed ==="
```

## Summary

The SecurityGateway ensures no REQUEST can bypass validation, but:
- **Application-layer security ≠ Infrastructure security**
- Network exposure is a bypass vector
- Defense in depth requires both layers
