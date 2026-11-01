import {describe, expect, it} from 'vitest';
import {parseUpstream} from './units';

describe('upstream peer input', () => {
  it('accepts one IP and preserves the routing choice', () => {
    expect(parseUpstream(' 192.0.2.5 ', '3128', true)).toEqual({address:'192.0.2.5', port:3128, exclusive:true});
    expect(parseUpstream('2001:DB8::53', '65535', false)).toEqual({address:'2001:db8::53', port:65535, exclusive:false});
  });
  it.each(['', 'proxy.example', 'https://192.0.2.5', '192.0.2.5/24', '192.0.2.5:3128', '192.0.2.5,192.0.2.6'])('rejects an invalid peer %s', address => {
    expect(parseUpstream(address, '3128', false)).toBeNull();
  });
  it.each(['', '0', '65536', '-1', '3.5', 'abc'])('rejects an invalid port %s', port => {
    expect(parseUpstream('192.0.2.5', port, true)).toBeNull();
  });
});
