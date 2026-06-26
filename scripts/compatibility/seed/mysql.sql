SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS compat_orders;
DROP TABLE IF EXISTS compat_users;
SET FOREIGN_KEY_CHECKS = 1;

CREATE TABLE compat_users (
  id int NOT NULL AUTO_INCREMENT,
  username varchar(128) NOT NULL,
  email varchar(255) NOT NULL,
  profile json NOT NULL,
  created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY compat_users_email_key (email),
  KEY compat_users_username_idx (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE compat_orders (
  id int NOT NULL AUTO_INCREMENT,
  user_id int NOT NULL,
  order_no varchar(64) NOT NULL,
  amount decimal(12,2) NOT NULL,
  attrs json NOT NULL,
  created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY compat_orders_order_no_key (order_no),
  KEY compat_orders_user_id_idx (user_id),
  CONSTRAINT compat_orders_user_fk FOREIGN KEY (user_id) REFERENCES compat_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

DELIMITER //
CREATE PROCEDURE compat_seed()
BEGIN
  DECLARE i INT DEFAULT 1;
  WHILE i <= 1000 DO
    INSERT INTO compat_users (username, email, profile, created_at)
    VALUES (
      CONCAT('用户-', i),
      CONCAT('user', i, '@example.test'),
      JSON_OBJECT('locale', 'zh-CN', 'emoji', '🚀', 'rank', i),
      DATE_SUB(NOW(), INTERVAL i MINUTE)
    );
    INSERT INTO compat_orders (user_id, order_no, amount, attrs, created_at)
    VALUES (
      i,
      CONCAT('ORD-', i),
      i * 1.23,
      JSON_OBJECT('channel', 'compat', 'note', '中文订单 😀', 'seq', i),
      DATE_SUB(NOW(), INTERVAL i SECOND)
    );
    SET i = i + 1;
  END WHILE;
END//
DELIMITER ;

CALL compat_seed();
DROP PROCEDURE compat_seed;
