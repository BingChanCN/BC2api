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

// CatProxyProviderConfig stores the credentials and defaults for a CatProxies provider.
type CatProxyProviderConfig struct {
	ent.Schema
}

func (CatProxyProviderConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "catproxy_provider_configs"},
	}
}

func (CatProxyProviderConfig) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (CatProxyProviderConfig) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.Enum("provider_type").
			Values("catproxies").
			Default("catproxies"),
		field.Enum("status").
			Values("active", "retiring", "disabled", "credential_error").
			Default("active"),
		field.Bool("is_default").
			Default(false),
		field.Enum("protocol").
			Values("http", "socks5h").
			Default("http"),
		field.String("host").
			MaxLen(255).
			NotEmpty(),
		field.String("base_username").
			MaxLen(255).
			NotEmpty(),
		field.String("password").
			MaxLen(255).
			NotEmpty().
			Sensitive(),
		field.String("default_country").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("default_state").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("default_city").
			MaxLen(100).
			Optional().
			Nillable(),
		field.Int("lifetime_minutes").
			Range(15, 1440).
			Default(60),
		field.Bool("strict").
			Default(true),
		field.Time("last_probe_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int("last_probe_latency_ms").
			Optional().
			Nillable(),
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

func (CatProxyProviderConfig) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("leases", ManagedProxyLease.Type),
	}
}

func (CatProxyProviderConfig) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name").Unique(),
		index.Fields("provider_type"),
		index.Fields("status"),
		index.Fields("is_default").Unique().Annotations(entsql.IndexWhere("provider_type = 'catproxies' AND is_default")),
	}
}
