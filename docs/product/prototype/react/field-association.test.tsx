import {it,expect} from 'vitest';
import {renderToStaticMarkup} from 'react-dom/server';
import {Field,SettingRow} from './components/kraken/blocks';
import {Input} from './components/ui/input';
it('общая подпись и помощь связаны с полем, в том числе внутри fragment',()=>{
 const html=renderToStaticMarkup(<SettingRow title="Объём" help="Мегабайты" control={<><Input defaultValue="64"/><span>МБ</span></>}/>);
 const id=html.match(/<input[^>]*id="([^"]+)"/)![1],label=html.match(/<label[^>]*id="([^"]+)"/)![1];
 expect(html).toContain('for="'+id+'"');expect(html).toContain('aria-labelledby="'+label+'"');expect(html).toMatch(/aria-describedby="[^"]+"/);
});
it('явная связь Field htmlFor сохраняется без дублирования id',()=>{
 const html=renderToStaticMarkup(<Field label="Имя" htmlFor="new-name"><Input/></Field>);
 expect(html).toContain('for="new-name"');expect(html).toContain('id="new-name"');
});
