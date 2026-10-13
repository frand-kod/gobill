-- Same data for both apps: one RADIUS hotspot plan, @CUSTOMERS@ active customers, one transaction each.
SET SESSION max_recursive_iterations = 100000;
INSERT INTO tbl_bandwidth (id,name_bw,rate_down,rate_down_unit,rate_up,rate_up_unit,burst) VALUES (1,'10M',10,'Mbps',10,'Mbps','');
INSERT INTO tbl_plans (id,name_plan,id_bw,price,type,typebp,validity,validity_unit,shared_users,routers,is_radius,enabled,prepaid,plan_type,device)
 VALUES (1,'Hotspot 30 Hari',1,'100000','Hotspot','Unlimited',30,'Days',1,'radius',1,1,'yes','Personal','Radius');
INSERT INTO tbl_customers (id,username,password,fullname,address,phonenumber,email,service_type,status,created_by)
 WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<@CUSTOMERS@)
 SELECT i, CONCAT('user',i), CONCAT('pass',i), CONCAT('Pelanggan ',i), 'Jl. Contoh', CONCAT('0812',LPAD(i,8,'0')), CONCAT('user',i,'@example.com'), 'Hotspot','Active',1 FROM n;
INSERT INTO tbl_user_recharges (customer_id,username,plan_id,namebp,recharged_on,recharged_time,expiration,time,status,method,routers,type,admin_id)
 SELECT id,username,1,'Hotspot 30 Hari',CURDATE(),CURTIME(),CURDATE()+INTERVAL 30 DAY,CURTIME(),'on','admin','radius','Hotspot',1 FROM tbl_customers;
INSERT INTO tbl_transactions (invoice,username,user_id,plan_name,price,recharged_on,recharged_time,expiration,time,method,routers,type,admin_id)
 SELECT CONCAT('INV-',id),username,id,'Hotspot 30 Hari','100000',CURDATE(),CURTIME(),CURDATE()+INTERVAL 30 DAY,CURTIME(),'admin','radius','Hotspot',1 FROM tbl_customers;
