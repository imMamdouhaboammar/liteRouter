# rules/default_routing.star
# ==============================================================================
# liteRouter Dynamic Starlark Routing Policy
# Inspired by Meta Buck2 / BXL (Build eXtension Language)
# ==============================================================================

def route_policy(ctx, candidates):
    """
    Evaluates incoming request context against candidate AI providers.
    Can set ROUTE_OVERRIDE to deterministically dictate the selected model.
    """
    # 1. VIP Tenant Emergency Override
    if ctx.get("tenant_tier") == "vip":
        return "claude-3-5-sonnet"

    # 2. Free Tier Budget Enforcer
    if ctx.get("tenant_tier") == "free":
        return "deepseek-v3"

    # Default: Return empty to allow Multi-Objective Pareto SPF Router to calculate
    return ""

# Exported default policy evaluation
ROUTE_OVERRIDE = ""
