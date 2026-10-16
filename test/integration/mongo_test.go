//go:build integration

package integration

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const mongoDB = "crm"

func mongoClient(t testing.TB, host string, auth bool) *mongo.Client {
	t.Helper()
	opts := options.Client().SetHosts([]string{host + ":27017"}).SetDirect(true).SetServerSelectionTimeout(5 * time.Second)
	if auth {
		opts.SetAuth(options.Credential{Username: "root", Password: env(t, "IT_MONGO_PASSWORD"), AuthSource: "admin"})
	}
	c, err := mongo.Connect(context.Background(), opts)
	check(t, err, "连接 MongoDB %s", host)
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })
	waitFor(t, "MongoDB "+host, 4*time.Minute, func(ctx context.Context) error {
		// 列库需要认证通过，比 Ping 更能说明初始化脚本已经跑完
		_, err := c.ListDatabaseNames(ctx, bson.D{})
		return err
	})
	return c
}

// seedMongo 写入虚构数据：嵌套文档、数组、日期、Decimal128、二进制、ObjectId、null，以及唯一索引和复合索引
func seedMongo(t testing.TB) {
	ctx := context.Background()
	c := mongoClient(t, hostMongo, true)
	db := c.Database(mongoDB)
	check(t, db.Drop(ctx), "删除 crm")
	rng := rand.New(rand.NewSource(99))
	var customers []any
	for i := 1; i <= 400; i++ {
		oid, _ := primitive.ObjectIDFromHex(fmt.Sprintf("65f0000000000000%08x", i))
		bal, _ := primitive.ParseDecimal128(fmt.Sprintf("%d.%02d", rng.Intn(100000), rng.Intn(100)))
		avatar := make([]byte, 16+rng.Intn(200))
		rng.Read(avatar)
		doc := bson.D{
			{Key: "_id", Value: oid},
			{Key: "name", Value: fmt.Sprintf("虚构客户 %d 🙂", i)},
			{Key: "email", Value: fmt.Sprintf("c%04d@example.invalid", i)},
			{Key: "tags", Value: bson.A{"vip", fmt.Sprintf("t%d", i%7), bson.D{{Key: "nested", Value: i}}}},
			{Key: "address", Value: bson.D{{Key: "city", Value: "示例市"}, {Key: "zip", Value: fmt.Sprintf("%06d", i)}}},
			{Key: "joined", Value: primitive.NewDateTimeFromTime(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour))},
			{Key: "balance", Value: bal},
			{Key: "avatar", Value: primitive.Binary{Subtype: 0, Data: avatar}},
			{Key: "active", Value: i%2 == 0},
		}
		if i%9 == 0 {
			doc = append(doc, bson.E{Key: "deleted_at", Value: nil})
		}
		customers = append(customers, doc)
	}
	_, err := db.Collection("customers").InsertMany(ctx, customers)
	check(t, err, "写入 customers")
	var events []any
	for i := 1; i <= 1200; i++ {
		events = append(events, bson.D{
			{Key: "_id", Value: int64(i)},
			{Key: "customer", Value: fmt.Sprintf("c%04d@example.invalid", 1+rng.Intn(400))},
			{Key: "kind", Value: []string{"login", "order", "refund"}[rng.Intn(3)]},
			{Key: "amount", Value: rng.Float64() * 1000},
			{Key: "at", Value: primitive.NewDateTimeFromTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute))},
		})
	}
	_, err = db.Collection("events").InsertMany(ctx, events)
	check(t, err, "写入 events")
	_, err = db.Collection("customers").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true).SetName("uniq_email")})
	check(t, err, "创建唯一索引")
	_, err = db.Collection("events").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "customer", Value: 1}, {Key: "at", Value: -1}}, Options: options.Index().SetName("customer_at")})
	check(t, err, "创建复合索引")
}

// restoreMongo 用 mongorestore 把下载的归档恢复到空白实例（归档里的命名空间就是 crm.*）
func restoreMongo(t testing.TB, archive []byte) {
	t.Helper()
	c := mongoClient(t, hostMongoScratch, false)
	check(t, c.Database(mongoDB).Drop(context.Background()), "清空空白实例的 crm")
	file := filepath.Join(t.TempDir(), mongoDB+".archive")
	check(t, os.WriteFile(file, archive, 0o600), "写入归档")
	cmd := exec.Command("mongorestore", "--host="+hostMongoScratch, "--port=27017", "--archive="+file, "--drop")
	out, err := cmd.CombinedOutput()
	check(t, err, "mongorestore 恢复下载的归档：%s", out)
}

// mongoContent 库的语义内容：每个集合按 _id 排序的全部文档（BSON 原始字节）和索引定义
func mongoContent(t testing.TB, host string, auth bool) map[string][]string {
	t.Helper()
	ctx := context.Background()
	db := mongoClient(t, host, auth).Database(mongoDB)
	names := must(db.ListCollectionNames(ctx, bson.D{}))(t)
	sort.Strings(names)
	out := map[string][]string{"collections": names}
	for _, n := range names {
		cur, err := db.Collection(n).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
		check(t, err, "读取集合 %s", n)
		var docs []string
		for cur.Next(ctx) {
			docs = append(docs, hex.EncodeToString(cur.Current))
		}
		check(t, cur.Err(), "遍历集合 %s", n)
		_ = cur.Close(ctx)
		out["docs:"+n] = docs

		icur, err := db.Collection(n).Indexes().List(ctx)
		check(t, err, "列出 %s 的索引", n)
		var idx []string
		for icur.Next(ctx) {
			var spec struct {
				Name   string `bson:"name"`
				Key    bson.D `bson:"key"`
				Unique bool   `bson:"unique"`
			}
			check(t, icur.Decode(&spec), "解析索引")
			idx = append(idx, fmt.Sprintf("%s %v unique=%t", spec.Name, spec.Key, spec.Unique))
		}
		sort.Strings(idx)
		out["indexes:"+n] = idx
	}
	return out
}
