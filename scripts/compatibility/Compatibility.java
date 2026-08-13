import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;

public final class Compatibility {
  public static void main(String[] args) throws Exception {
    String host = value("DATABASE_COMPAT_HOST", "127.0.0.1");
    String port = value("DATABASE_COMPAT_PORT", "3306");
    String user = value("DATABASE_COMPAT_USER", "admin");
    String password = value("DATABASE_COMPAT_PASSWORD", "");
    String url = "jdbc:mysql://" + host + ":" + port + "/?sslMode=REQUIRED&useServerPrepStmts=true&useLocalSessionState=true";
    try (Connection connection = DriverManager.getConnection(url, user, password)) {
      try (Statement statement = connection.createStatement(); ResultSet result = statement.executeQuery("SELECT VERSION()")) {
        result.next();
        if (!"8.4.11-database-0.2.0-dev".equals(result.getString(1))) {
          throw new SQLException("unexpected version " + result.getString(1));
        }
      }
      try (Statement statement = connection.createStatement()) {
        statement.execute("CREATE DATABASE IF NOT EXISTS compatibility");
        statement.execute("USE compatibility");
        statement.execute("SET time_zone = '+05:30'");
      }
      try (PreparedStatement statement = connection.prepareStatement("SELECT ? AS name, NULL AS empty, 7 AS number")) {
        statement.setString(1, "Ada");
        try (ResultSet result = statement.executeQuery()) {
          result.next();
          if (!"Ada".equals(result.getString(1)) || result.getObject(2) != null || result.getInt(3) != 7) {
            throw new SQLException("prepared result mismatch");
          }
        }
      }
      try (Statement statement = connection.createStatement()) {
        try {
          statement.executeQuery("SELECT * FROM no_such_table");
          throw new SQLException("unknown-table query unexpectedly succeeded");
        } catch (SQLException error) {
          if (error.getErrorCode() != 1146 || !"42S02".equals(error.getSQLState())) {
            throw error;
          }
        }
      }
    }
    System.out.println("java-connector-j ok");
  }

  private static String value(String name, String fallback) {
    String value = System.getenv(name);
    return value == null || value.isEmpty() ? fallback : value;
  }
}
