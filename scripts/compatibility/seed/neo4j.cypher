MATCH (n) DETACH DELETE n;

UNWIND range(1, 1000) AS i
CREATE (:CompatUser:Person {
  id: i,
  name: '用户-' + toString(i),
  locale: 'zh-CN',
  emoji: '🚀',
  createdAt: datetime()
});

UNWIND range(1, 1000) AS i
MATCH (u:CompatUser {id: i})
CREATE (o:CompatOrder:Order {
  id: i,
  orderNo: 'ORD-' + toString(i),
  amount: i * 1.23,
  note: '中文订单 😀',
  createdAt: datetime()
})
CREATE (u)-[:PLACED {source: 'compat', seq: i}]->(o);

MATCH (a:CompatUser {id: 1}), (b:CompatUser {id: 2})
CREATE (a)-[:KNOWS {since: date()}]->(b);
