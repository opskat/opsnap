//go:build integration

package integration

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisDBs 种子数据分布在两个逻辑库里，备份的是整个实例
var redisDBs = []int{0, 3}

func redisClient(t testing.TB, host, password string, db int) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: host + ":6379", Password: password, DB: db})
	t.Cleanup(func() { _ = c.Close() })
	waitFor(t, "Redis "+host, 2*time.Minute, func(ctx context.Context) error { return c.Ping(ctx).Err() })
	return c
}

// seedRedis 写入虚构数据：二进制字符串、整数、大小哈希、列表、集合、有序集合、流、带过期时间的键，分布在 db0 与 db3
func seedRedis(t testing.TB) {
	ctx := context.Background()
	pw := env(t, "IT_REDIS_PASSWORD")
	c0 := redisClient(t, hostRedis, pw, 0)
	check(t, c0.FlushAll(ctx).Err(), "清空 Redis")
	rng := rand.New(rand.NewSource(7))
	bin := make([]byte, 4096)
	rng.Read(bin)
	bin[0], bin[1] = 0, '\n'
	p := c0.Pipeline()
	p.Set(ctx, "str:plain", "你好，opsnap 🙂", 0)
	p.Set(ctx, "str:binary", bin, 0)
	p.Set(ctx, "counter", 424242, 0)
	p.Set(ctx, "session:abc", "临时会话", time.Hour)
	for i := 0; i < 5; i++ {
		p.HSet(ctx, "user:small", fmt.Sprintf("f%d", i), fmt.Sprintf("值 %d", i))
	}
	for i := 0; i < 600; i++ { // 超过 listpack 阈值，落成 hashtable 编码
		p.HSet(ctx, "user:large", fmt.Sprintf("field-%04d", i), strings.Repeat("v", i%70))
	}
	for i := 0; i < 300; i++ {
		p.RPush(ctx, "queue:jobs", fmt.Sprintf("job-%d", i))
	}
	for i := 0; i < 200; i++ {
		p.SAdd(ctx, "set:tags", fmt.Sprintf("tag-%d", i))
	}
	for i := 0; i < 250; i++ {
		p.ZAdd(ctx, "zset:board", redis.Z{Score: float64(rng.Intn(100000)) / 7, Member: fmt.Sprintf("玩家-%d", i)})
	}
	for i := 1; i <= 100; i++ {
		p.XAdd(ctx, &redis.XAddArgs{Stream: "stream:events", ID: fmt.Sprintf("1700000000000-%d", i), Values: []string{"type", "login", "user", fmt.Sprint(i)}})
	}
	_, err := p.Exec(ctx)
	check(t, err, "写入 Redis db0")

	c3 := redisClient(t, hostRedis, pw, 3)
	for i := 0; i < 50; i++ {
		check(t, c3.Set(ctx, fmt.Sprintf("cache:%02d", i), fmt.Sprintf("缓存 %d", i), 0).Err(), "写入 Redis db3")
	}
}

// restoreRedis 把下载的 RDB 放进空白实例的数据目录，再让它不保存、直接重新加载这个文件
func restoreRedis(t testing.TB, rdb []byte) {
	t.Helper()
	ctx := context.Background()
	c := redisClient(t, hostRedisScratch, "", 0)
	tmp := filepath.Join(redisScratchDir, "dump.rdb.tmp")
	check(t, os.WriteFile(tmp, rdb, 0o644), "写入 RDB")
	check(t, os.Rename(tmp, filepath.Join(redisScratchDir, "dump.rdb")), "替换 RDB")
	check(t, c.Do(ctx, "DEBUG", "RELOAD", "NOSAVE").Err(), "空白 Redis 重新加载 RDB")
}

// redisContent 实例的语义内容：每个库每个键的类型和值；过期时间只比较「有 / 没有」
func redisContent(t testing.TB, host, password string) map[string][]string {
	t.Helper()
	ctx := context.Background()
	out := map[string][]string{}
	for db := 0; db < 16; db++ {
		c := redisClient(t, host, password, db)
		keys, err := c.Keys(ctx, "*").Result()
		check(t, err, "列出 db%d 的键", db)
		if len(keys) == 0 {
			continue
		}
		sort.Strings(keys)
		out[fmt.Sprintf("db%d:keys", db)] = keys
		for _, k := range keys {
			typ := must(c.Type(ctx, k).Result())(t)
			var vals []string
			switch typ {
			case "string":
				vals = []string{must(c.Get(ctx, k).Result())(t)}
			case "hash":
				for f, v := range must(c.HGetAll(ctx, k).Result())(t) {
					vals = append(vals, f+"="+v)
				}
				sort.Strings(vals)
			case "list":
				vals = must(c.LRange(ctx, k, 0, -1).Result())(t)
			case "set":
				vals = must(c.SMembers(ctx, k).Result())(t)
				sort.Strings(vals)
			case "zset":
				for _, z := range must(c.ZRangeWithScores(ctx, k, 0, -1).Result())(t) {
					vals = append(vals, fmt.Sprintf("%v=%v", z.Member, z.Score))
				}
			case "stream":
				for _, m := range must(c.XRange(ctx, k, "-", "+").Result())(t) {
					vals = append(vals, fmt.Sprintf("%s %v", m.ID, m.Values))
				}
			default:
				t.Fatalf("种子数据里没有 %s 类型的键 %s", typ, k)
			}
			ttl := must(c.PTTL(ctx, k).Result())(t)
			out[fmt.Sprintf("db%d:%s:%s ttl=%t", db, k, typ, ttl > 0)] = vals
		}
	}
	return out
}
