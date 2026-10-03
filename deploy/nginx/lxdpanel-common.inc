    server_name _;
    root /opt/lxdpanel;
    index login.html index.html;
    charset utf-8;
    client_max_body_size 100m;

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $proxy_xfp;

    location /api/ { proxy_pass http://127.0.0.1:8081; }
    location = /api/public/brand {
        rewrite ^ /api/public/brand-settings break;
        proxy_pass http://127.0.0.1:8081;
    }
    location /ws/ {
        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
    location /admin      { proxy_pass http://127.0.0.1:8081; }
    location /user       { proxy_pass http://127.0.0.1:8081; }
    location /container/ { proxy_pass http://127.0.0.1:8081; }
    location = /console  { proxy_pass http://127.0.0.1:8081; }
    location /swagger/   { proxy_pass http://127.0.0.1:8081; }
    location /static/    { proxy_pass http://127.0.0.1:8081; }

    location / { try_files $uri $uri/ /login.html; }
