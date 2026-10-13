# PHPNuxBill runtime for bench/run.sh: official PHP image, the extensions PHPNuxBill needs, opcache on.
FROM docker.io/library/php:8.2-apache
RUN apt-get update && apt-get install -y libpng-dev libjpeg62-turbo-dev libfreetype6-dev libzip-dev \
 && docker-php-ext-configure gd --with-freetype --with-jpeg \
 && docker-php-ext-install -j$(nproc) gd pdo_mysql mysqli zip opcache \
 && cp /usr/local/etc/php/php.ini-production /usr/local/etc/php/php.ini \
 && rm -rf /var/lib/apt/lists/*
