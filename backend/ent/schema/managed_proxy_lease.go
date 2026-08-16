package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ManagedProxyLease binds one account to one provider-managed proxy session.
type ManagedProxyLease struct {
	ent.Schema
}

func (ManagedProxyLease) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "managed_proxy_leases"},
	}
}

func (ManagedProxyLease) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ManagedProxyLease) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id"),
		field.Int64("proxy_id"),
		field.Int64("provider_config_id"),
		field.String("session_id").
			MaxLen(255).
			NotEmpty(),
		field.String("target_country").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("target_state").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("target_city").
			MaxLen(100).
			Optional().
			Nillable(),
		field.Bool("strict"),
		field.Int("lifetime_minutes").
			Range(15, 1440),
		field.Enum("state").
			Values("pending", "active", "rotating", "expired", "failed", "released").
			Default("pending"),
		field.Enum("health_status").
			Values("unknown", "healthy", "degraded", "unhealthy").
			Default("unknown"),
		field.Time("health_checked_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("observed_exit_ip").
			MaxLen(45).
			Optional().
			Nillable(),
		field.String("observed_country").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("observed_state").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("observed_city").
			MaxLen(100).
			Optional().
			Nillable(),
		field.Int("observed_latency_ms").
			Optional().
			Nillable(),
		field.Time("activated_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("last_rotated_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("next_rotation_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("expires_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int("failure_count").
			Default(0),
		field.Int("consecutive_failure_count").
			Default(0),
		field.String("last_error").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("last_error_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ManagedProxyLease) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", Account.Type).
			Ref("managed_proxy_lease").
			Field("account_id").
			Required().
			Unique().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("proxy", Proxy.Type).
			Ref("managed_proxy_leases").
			Field("proxy_id").
			Required().
			Unique().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("provider_config", CatProxyProviderConfig.Type).
			Ref("leases").
			Field("provider_config_id").
			Required().
			Unique().
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (ManagedProxyLease) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id").Unique(),
		index.Fields("proxy_id").Unique(),
		index.Fields("provider_config_id"),
		index.Fields("state"),
		index.Fields("health_status"),
		index.Fields("next_rotation_at"),
		index.Fields("expires_at"),
		index.Fields("provider_config_id", "state"),
	}
}
