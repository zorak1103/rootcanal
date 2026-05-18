FROM alpine:3.20

ARG USER=testuser
ARG PASS=hunter2hunter2

RUN apk add --no-cache openssh-server openssh-sftp-server bash && \
    ssh-keygen -A && \
    chmod 600 /etc/ssh/ssh_host_*_key && \
    adduser -D -s /bin/bash ${USER} && \
    echo "${USER}:${PASS}" | chpasswd && \
    mkdir -p /home/${USER}/.ssh && \
    chmod 700 /home/${USER}/.ssh && \
    chown ${USER}:${USER} /home/${USER}/.ssh && \
    mkdir -p /srv/sftp

COPY sshd/sshd_config /etc/ssh/sshd_config
COPY seed/ /srv/sftp/

RUN chown -R ${USER}:${USER} /srv/sftp && \
    find /srv/sftp -type d -exec chmod 755 {} + && \
    find /srv/sftp -type f -exec chmod 644 {} +

COPY sshd/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 22
ENTRYPOINT ["/entrypoint.sh"]
