FROM nginx:alpine

# Instalar openssl para validação de certificados
RUN apk add --no-cache openssl

# Copiar configuração principal do nginx (arquivo original é .txt)
COPY nginx.conf.txt /etc/nginx/conf.d/default.conf

# Criar diretórios necessários
RUN mkdir -p /etc/nginx/conf.d/dynamic /var/www/html

EXPOSE 80 443

CMD ["nginx", "-g", "daemon off;"]
