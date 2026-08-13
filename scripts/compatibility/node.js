'use strict';

const mysql = require('mysql2/promise');

async function main() {
  const connection = await mysql.createConnection({
    host: process.env.DATABASE_COMPAT_HOST || '127.0.0.1',
    port: Number(process.env.DATABASE_COMPAT_PORT || 3306),
    user: process.env.DATABASE_COMPAT_USER || 'admin',
    password: process.env.DATABASE_COMPAT_PASSWORD || '',
    database: undefined,
    ssl: process.env.DATABASE_COMPAT_TLS === '1' ? { rejectUnauthorized: false } : undefined,
  });
  try {
    const [version] = await connection.query('SELECT VERSION()');
    if (version[0]['VERSION()'] !== '8.4.11-database-0.2.0-dev') {
      throw new Error(`unexpected version ${version[0]['VERSION()']}`);
    }
    await connection.query('CREATE DATABASE IF NOT EXISTS compatibility');
    await connection.query('USE compatibility');
    await connection.query("SET time_zone = '+05:30'");
    const [rows] = await connection.execute('SELECT ? AS name, NULL AS empty, 7 AS number', ['Ada']);
    if (rows[0].name !== 'Ada' || rows[0].empty !== null || rows[0].number !== 7) {
      throw new Error(`prepared result mismatch: ${JSON.stringify(rows)}`);
    }
    try {
      await connection.query('SELECT * FROM no_such_table');
      throw new Error('unknown-table query unexpectedly succeeded');
    } catch (error) {
      if (error.code !== 'ER_NO_SUCH_TABLE' || error.errno !== 1146 || error.sqlState !== '42S02') {
        throw error;
      }
    }
  } finally {
    await connection.end();
  }
  process.stdout.write('node-mysql2 ok\n');
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
