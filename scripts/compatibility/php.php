<?php
declare(strict_types=1);

$host = getenv('DATABASE_COMPAT_HOST') ?: '127.0.0.1';
$port = getenv('DATABASE_COMPAT_PORT') ?: '3306';
$user = getenv('DATABASE_COMPAT_USER') ?: 'admin';
$password = getenv('DATABASE_COMPAT_PASSWORD') ?: '';
$dsn = "mysql:host={$host};port={$port};charset=utf8mb4";
$pdoOptions = [
    PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
    PDO::ATTR_EMULATE_PREPARES => false,
];
if (getenv('DATABASE_COMPAT_TLS') === '1') {
    $pdoOptions[PDO::MYSQL_ATTR_SSL_VERIFY_SERVER_CERT] = false;
}

$pdo = new PDO($dsn, $user, $password, $pdoOptions);
$version = (string) $pdo->query('SELECT VERSION()')->fetchColumn();
if ($version !== '8.4.11-database-0.2.0-dev') {
    throw new RuntimeException("unexpected version {$version}");
}
$pdo->exec('CREATE DATABASE IF NOT EXISTS compatibility');
$pdo->exec('USE compatibility');
$pdo->exec("SET time_zone = '+05:30'");
$statement = $pdo->prepare('SELECT ? AS name, NULL AS empty, 7 AS number');
$statement->execute(['Ada']);
$row = $statement->fetch(PDO::FETCH_NUM);
if ($row !== ['Ada', null, 7]) {
    throw new RuntimeException('PDO prepared result mismatch: ' . json_encode($row));
}
$error = false;
try {
    $pdo->query('SELECT * FROM no_such_table');
} catch (PDOException $exception) {
    $error = str_contains($exception->getMessage(), '1146');
}
if (!$error) {
    throw new RuntimeException('PDO did not preserve unknown-table error');
}
$pdo = null;

$mysqli = mysqli_init();
if (getenv('DATABASE_COMPAT_TLS') === '1') {
    $mysqli->ssl_set(null, null, null, null, null);
}
if (!$mysqli || !$mysqli->real_connect($host, $user, $password, null, (int) $port, null, MYSQLI_CLIENT_SSL) || $mysqli->connect_errno) {
    throw new RuntimeException('mysqli connection failed: ' . mysqli_connect_error());
}
$mysqli->query('CREATE DATABASE IF NOT EXISTS compatibility');
$mysqli->select_db('compatibility');
$result = $mysqli->query('SELECT VERSION()');
if (!$result || $result->fetch_row()[0] !== '8.4.11-database-0.2.0-dev') {
    throw new RuntimeException('mysqli version query failed');
}
$statement = $mysqli->prepare('SELECT ? AS name, NULL AS empty, 7 AS number');
if (!$statement) {
    throw new RuntimeException('mysqli prepare failed: ' . $mysqli->error);
}
$value = 'Ada';
$statement->bind_param('s', $value);
$statement->execute();
$result = $statement->get_result();
$row = $result ? $result->fetch_row() : null;
if ($row === null || $row[0] !== 'Ada' || $row[1] !== null || (int) $row[2] !== 7) {
    throw new RuntimeException('mysqli prepared result mismatch');
}
$mysqli->close();
echo "php-pdo-mysqli ok\n";
