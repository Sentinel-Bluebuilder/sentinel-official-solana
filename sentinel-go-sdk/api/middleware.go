package api

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jellydator/ttlcache/v3"
	"github.com/solana-foundation/solana-go/v2"
	"golang.org/x/time/rate"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
)

const (
	limiterTTL = 10 * time.Minute
	signerKey  = "sentinel.signer"
)

type Limiter struct {
	burst   int
	entries *ttlcache.Cache[string, *rate.Limiter]
	rate    rate.Limit
}

func NewLimiter(perSecond float64, burst int) *Limiter {
	entries := ttlcache.New(ttlcache.WithTTL[string, *rate.Limiter](limiterTTL))

	go entries.Start()

	return &Limiter{
		burst:   burst,
		entries: entries,
		rate:    rate.Limit(perSecond),
	}
}

func (l *Limiter) Allow(key string) (retryAfterSeconds int, ok bool) {
	bucket := l.bucket(key)
	now := time.Now()

	if bucket.AllowN(now, 1) {
		return 0, true
	}

	return secondsUntilToken(bucket.TokensAt(now), l.rate), false
}

func (l *Limiter) Stop() {
	l.entries.Stop()
}

func (l *Limiter) bucket(key string) *rate.Limiter {
	if item := l.entries.Get(key); item != nil {
		return item.Value()
	}

	item, _ := l.entries.GetOrSet(
		key,
		rate.NewLimiter(l.rate, l.burst),
		ttlcache.WithTTL[string, *rate.Limiter](ttlcache.DefaultTTL),
	)

	return item.Value()
}

func LimitByIP(limiter *Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit(c, limiter, c.ClientIP())
	}
}

func LimitBySigner(limiter *Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if signer, ok := SignerFrom(c); ok {
			limit(c, limiter, signer.String())

			return
		}

		c.Next()
	}
}

func RequireSignature(seen *ttlcache.Cache[auth.Nonce, struct{}]) gin.HandlerFunc {
	return func(c *gin.Context) {
		sig, err := auth.RequestSignatureFromHeaders(c.Request.Header)
		if err != nil {
			Abort(c, err)

			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			Abort(c, err)

			return
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		if err := auth.VerifyRequest(sig, c.Request.Method, c.Request.URL.Path, body, time.Now(), seen); err != nil {
			Abort(c, err)

			return
		}

		c.Set(signerKey, sig.Signer)
		c.Next()
	}
}

func RequireSigner(ctx context.Context) (solana.PublicKey, error) {
	key, ok := SignerFrom(ctx)
	if !ok {
		return solana.PublicKey{}, ErrUnauthorized
	}

	return key, nil
}

func SignerFrom(ctx context.Context) (solana.PublicKey, bool) {
	value := ctx.Value(signerKey)

	key, ok := value.(solana.PublicKey)

	return key, ok
}

func limit(ginCtx *gin.Context, limiter *Limiter, key string) {
	if retryAfter, ok := limiter.Allow(key); !ok {
		ginCtx.Header(retryAfterHeader, strconv.Itoa(retryAfter))
		Abort(ginCtx, ErrRateLimited)

		return
	}

	ginCtx.Next()
}

func limitRequestBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			Abort(c, ErrRequestTooLarge)

			return
		}

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

func secondsUntilToken(tokens float64, rateLimit rate.Limit) int {
	secs := math.Ceil((1 - tokens) / float64(rateLimit))

	return int(min(max(secs, 1), math.MaxInt32))
}
