// Cordis entry: registers the TRANSAQ gateway as the `ru`/`finam` provider in dsh-trading
// and exposes read-only ru_* tools. Registries are optional services of the host.

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

  const registerIn = (key, service) => {
    const registry = ctx.get?.(key, false);
    if (!registry) return;
    registered = true;
    if (typeof ctx.effect === 'function') ctx.effect(() => registry.register('ru', 'finam', service));
    else registry.register('ru', 'finam', service);
  };
  registerIn('tradingMarketDataRegistry', market);
  registerIn('tradingTradeRegistry', trade);

  if (typeof ctx.tools?.register === 'function') {
    registered = true;
    for (const def of buildTools({ market, trade })) track(ctx.tools.register(def));
  }

  if (!registered) {
    ctx.logger?.('transaq')?.warn?.('transaq connector: no dsh-trading registries or tools service in this host');
  }
}
