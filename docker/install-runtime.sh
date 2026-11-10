#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates tzdata wget gosu poppler-utils libreoffice-calc libreoffice-impress default-jre-headless tesseract-ocr tesseract-ocr-rus tesseract-ocr-eng ffmpeg
rm -rf /var/lib/apt/lists/*
# The official Ubuntu image may contain its default UID 1000 account.
# Preserve the existing application's UID/GID for mounted file ownership.
if id ubuntu >/dev/null 2>&1; then
  userdel -r ubuntu
fi
groupadd -g 1000 app
useradd -m -u 1000 -g app -s /usr/sbin/nologin app
