import os

import mysql.connector


def main() -> None:
    connection = mysql.connector.connect(
        host=os.environ.get("DATABASE_COMPAT_HOST", "127.0.0.1"),
        port=int(os.environ.get("DATABASE_COMPAT_PORT", "3306")),
        user=os.environ.get("DATABASE_COMPAT_USER", "admin"),
        password=os.environ.get("DATABASE_COMPAT_PASSWORD", ""),
        use_pure=True,
        ssl_disabled=os.environ.get("DATABASE_COMPAT_TLS") != "1",
        ssl_verify_cert=False,
        ssl_verify_identity=False,
    )
    try:
        cursor = connection.cursor()
        cursor.execute("SELECT VERSION()")
        if cursor.fetchone()[0] != "8.4.11-database-0.2.0-dev":
            raise RuntimeError("unexpected version")
        cursor.execute("CREATE DATABASE IF NOT EXISTS compatibility")
        cursor.execute("USE compatibility")
        cursor.execute("SET time_zone = '+05:30'")
        prepared = connection.cursor(prepared=True)
        prepared.execute("SELECT %s AS name, NULL AS empty, 7 AS number", ("Ada",))
        if prepared.fetchone() != ("Ada", None, 7):
            raise RuntimeError("prepared result mismatch")
        try:
            cursor.execute("SELECT * FROM no_such_table")
            raise RuntimeError("unknown-table query unexpectedly succeeded")
        except mysql.connector.Error as error:
            if error.errno != 1146 or error.sqlstate != "42S02":
                raise
    finally:
        connection.close()
    print("python-connector ok")


if __name__ == "__main__":
    main()
