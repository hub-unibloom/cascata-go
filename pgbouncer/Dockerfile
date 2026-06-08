# PgBouncer com entrypoint personalizado e logs detalhados
FROM edoburu/pgbouncer:latest

# Instalar ferramentas de debug
USER root
RUN apk add --no-cache bind-tools net-tools iputils

# Copiar entrypoint personalizado
COPY entrypoint.sh /usr/local/bin/pgbouncer-entrypoint.sh
RUN chmod +x /usr/local/bin/pgbouncer-entrypoint.sh

# Voltar para o usuário postgres
USER postgres

ENTRYPOINT ["/usr/local/bin/pgbouncer-entrypoint.sh"]
CMD ["/usr/bin/pgbouncer", "/etc/pgbouncer/pgbouncer.ini"]
