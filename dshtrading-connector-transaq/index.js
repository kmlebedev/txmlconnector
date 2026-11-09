// Cordis entry: registers the TRANSAQ gateway as the `ru`/`finam` provider in dsh-trading
// and exposes read-only ru_* tools. The registries and the tools service are
// optional services of the host: each is used when (or as soon as) it appears.

import { resolveConfig } from './config.js';
import { createGatewayClient } from './gateway-client.js';
import { createSymbolResolver } from './symbols.js';
import { createMarketDataService } from './market-data.js';
import { createTradeService } from './trade.js';
import { buildTools } from './tools.js';

export const name = 'dsh-trading-ru-connector-transaq';
export const inject = [];

/**
 * @param {object} ctx Cordis context
 * @param {object} [rawConfig] profile row config, passed as-is by the loader
 */
export function apply(ctx, rawConfig) {
  const config = resolveConfig(rawConfig);
  if (!config.enabled) return;

  // Resolved on every request so rotated tokens are picked up without a restart.
  const resolveToken = async () => {
    try {
      const v = (await ctx.get?.('credentials', false)?.resolve(config.tokenRef))?.value;
      if (typeof v === 'string' && v !== '') return v;
    } catch {
      // fall back to the environment
    }
    return process.env[config.tokenRef];
  };

  const client = createGatewayClient({ config, resolveToken });
  const resolver = createSymbolResolver(async () => (await client.get('/instruments')).instruments);
  const market = createMarketDataService({ client, resolver, pollMs: config.pollMs });
  const trade = createTradeService({ client, resolver });

  const track = (disposer) => {
    if (typeof disposer !== 'function') return;
    if (typeof ctx.effect === 'function') ctx.effect(() => disposer);
  };
  let registered = false;

  // The host's router row provides these registries, but it may not be ready at
  // apply() time: wait for the service (Cordis ctx.inject) instead of sampling
  // ctx.get() once and silently skipping the registration.
  const registerIn = (key, service) => {
    const register = () => {
      const registry = ctx.get?.(key, false);
      if (!registry) return;
      registered = true;
      if (typeof ctx.effect === 'function') ctx.effect(() => registry.register('ru', 'finam', service));
      else registry.register('ru', 'finam', service);
    };
    if (typeof ctx.inject === 'function') {
      // The host can provide the service later; count the wait as a registration
      // intent so the "nothing to register" warning is not premature.
      registered = true;
      ctx.inject([key], register);
    } else {
      register();
    }
  };
  registerIn('tradingMarketDataRegistry', market);
  registerIn('tradingTradeRegistry', trade);

  // tools is an optional host service (plan Task 7). Wait for it through
  // ctx.inject instead of declaring a static dependency, so a host without the
  // tools service still gets the market-data/trade registrations.
  const registerTools = () => {
    // ctx.get is the Cordis way; a plain context may carry the service as a
    // property (legacy/embedded hosts), so fall back to ctx.tools.
    const toolsSvc = ctx.get?.('tools', false) ?? ctx.tools;
    if (typeof toolsSvc?.register !== 'function') return;
    registered = true;
    for (const def of buildTools({ market, trade })) track(toolsSvc.register(def));
  };
  if (typeof ctx.inject === 'function') {
    // Waiting for the optional service counts as a registration intent: the
    // "nothing to register" warning must not fire while the host is still
    // wiring its services.
    registered = true;
    ctx.inject(['tools'], registerTools);
  } else {
    registerTools();
  }

  if (!registered) {
    ctx.logger?.('transaq')?.warn?.('transaq connector: no dsh-trading registries or tools service in this host');
  }
}
