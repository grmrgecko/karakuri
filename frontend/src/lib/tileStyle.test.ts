import { describe, expect, it } from 'vitest';
import { isTransparent, safeColor, tileStyleCSS } from './tileStyle';

// A placement's style becomes declarations on its cell and the theme custom
// properties widgets read through their shadow roots.
describe('tileStyleCSS', () => {
  it('adds nothing for an empty style', () => {
    expect(tileStyleCSS(undefined)).toBe('');
    expect(tileStyleCSS({})).toBe('');
  });

  it('drops the face, border, and shadow of a transparent tile', () => {
    const css = tileStyleCSS({ background: 'transparent' });
    expect(css).toContain('background:transparent');
    expect(css).toContain('border-color:transparent');
    expect(css).toContain('box-shadow:none');
    expect(css).toContain('--theme-widget-bg:transparent');
    expect(isTransparent({ background: 'Transparent' })).toBe(true);
  });

  it('keeps an explicit border on a transparent tile', () => {
    const css = tileStyleCSS({ background: 'transparent', border_width: 2, border_color: '#f80' });
    expect(css.indexOf('border:2px solid #f80')).toBeGreaterThan(css.indexOf('border-color:transparent'));
  });

  it('draws a border of the given width and color', () => {
    expect(tileStyleCSS({ border_color: '#ff8800', border_width: 3 })).toContain('border:3px solid #ff8800');
  });

  it('makes a square tile from radius 0 and leaves an unset radius to the theme', () => {
    expect(tileStyleCSS({ radius: 0 })).toContain('border-radius:0px');
    expect(tileStyleCSS({ border_width: 1 })).not.toContain('border-radius');
  });

  it('sets the theme text and accent colors widgets read', () => {
    const css = tileStyleCSS({ text_color: 'white', accent_color: 'rgb(1, 2, 3)' });
    expect(css).toContain('--theme-text:white');
    expect(css).toContain('--theme-accent:rgb(1, 2, 3)');
  });

  it('ignores a value that could end the declaration or load a resource', () => {
    expect(safeColor('red; position:fixed')).toBe('');
    expect(safeColor('url(http://example.com/x)')).toBe('');
    expect(tileStyleCSS({ background: 'red;}' })).toBe('');
  });
});
