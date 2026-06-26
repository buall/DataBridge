const dbName = process.env.COMPAT_DB || "compat_source";
const database = db.getSiblingDB(dbName);
database.dropDatabase();

const users = [];
const orders = [];
for (let i = 1; i <= 1000; i += 1) {
  users.push({
    _id: ObjectId(),
    username: `用户-${i}`,
    profile: {
      locale: "zh-CN",
      emoji: "🚀",
      rank: i,
      nested: { enabled: true, score: i * 1.5 },
    },
    tags: ["compat", "mongo", `batch-${i % 10}`],
    createdAt: new Date(Date.now() - i * 60000),
  });
}
database.compat_users.insertMany(users);
for (let i = 1; i <= 1000; i += 1) {
  orders.push({
    userId: users[i - 1]._id,
    orderNo: `ORD-${i}`,
    amount: NumberDecimal((i * 1.23).toFixed(2)),
    items: [{ sku: `SKU-${i}`, qty: (i % 5) + 1 }],
    note: "中文订单 😀",
    createdAt: new Date(Date.now() - i * 1000),
  });
}
database.compat_orders.insertMany(orders);
database.compat_users.createIndex({ username: 1 }, { unique: true });
database.compat_orders.createIndex({ orderNo: 1 }, { unique: true });
