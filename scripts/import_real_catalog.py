#!/usr/bin/env python3
"""Replace identified legacy/demo rows with the collected, attributed catalog.

Usage: python3 scripts/import_real_catalog.py [--apply]
Back up the database and stop backend/worker before --apply. Unknown accounts survive.
This imports sourced product facts and review excerpts; editorial personas, notes,
stock quantities, relationships and addresses are explicitly local demo content.
"""
import argparse
import datetime
import html
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
DATA = ROOT / 'data' / 'real-catalog'
PUBLIC = ROOT / 'frontend' / 'public' / 'catalog'
PASSWORD_HASH = '$2y$10$qYYSI.G8VQRm/BggiBYkp.TBmWaF1V2SUdsFsyCqnTCFT6tp/2MEy'
PEOPLE = [
 ('阿栗的厨房','把普通早餐过得认真一点。杯子、食谱，还有窗边的光。'),
 ('阿遥的灯光','收集黄昏和灯光，喜欢有一点旧感的家。'),
 ('七七慢慢住','一个人的小家，也可以有很多温暖的角落。'),
 ('花与空瓶','花会凋谢，想把看见它时的心情留下来。'),
 ('米粒的便当','整理厨房，也整理每一天的心情。'),
 ('桌边的林同学','认真生活，也认真给桌面做减法。'),
 ('白桃乌龙','不赶路的时候，记得看看窗外。'),
 ('余温','喜欢柔软的东西，和不被打扰的周末。'),
 ('林间来信','用照片记录家的变化。'),
 ('一只小梨','不着急添置，慢慢找到自己喜欢的。'),
 ('晚风有信','一些关于房间、花和日常的碎碎念。'),
 ('橘子汽水','理想的生活，是冰箱里有水果，桌上有花。'),
 ('小满同学','拾物体验账号。头像为示意照片，地址与交易仅供本地演示。'),
 ('小周不加班','下班之后，留一点时间给自己。'),
 ('好好吃饭的圆圆','食物热着，生活也热着。'),
 ('早起的面包','明天早上想给自己做顿好吃的。'),
 ('山下的海','多一点出门，也多一点好好待在家。'),
 ('棉花糖不甜','不完美的日常，也想认真记录。'),
 ('陈陈的书桌','书桌是家里最小的一座岛。'),
 ('安安今天也在','普通日子里的一点小发现。'),
 ('青柠苏打','记录生活，而不是标准答案。'),
 ('南南','愿每个平凡的今天都有值得记住的小事。'),
 ('小岛来客','有时，一件小东西就能让房间不一样。'),
 ('周末观察员','对周末的要求：舒服，简单，不赶时间。'),
]
LOCAL_NAMES = {
 '40503626':('GLADELIG 蓝色马克杯','蓝色 · 陶瓷 · 13 盎司'),
 '50603687':('TOBISFISK 米色图案马克杯','米色图案 · 17 盎司'),
 '60143992':('FÄRGRIK 白色炻器马克杯','白色 · 炻器 · 8.5 盎司'),
 '80278367':('IKEA 365+ 透明玻璃马克杯','透明玻璃 · 12 盎司'),
 '00404987':('DEJSA 欧泊玻璃台灯','米色 / 欧泊玻璃 · 美规款'),
 '60573576':('STORSEGEL 白蜡木台灯','白蜡木 / 白色 · 美规款'),
 '70096377':('FADO 球形玻璃台灯','白色 · 美规款 · 10 英寸'),
 '80321380':('ÅRSTID 黄铜色台灯','黄铜色 / 白色 · 美规款'),
 '20624592':('DAGGKÅPOR 浅米色休闲毯','浅米色 · 51 × 67 英寸'),
 '50633552':('KLIPPOXEL 浅蓝色休闲毯','浅蓝色 · 51 × 67 英寸'),
 '10592365':('STILREN 白色花瓶','白色 · 高 4¾ 英寸'),
 '20580324':('IKEA 365+ 不锈钢食物盒','方形 · 不锈钢 · 20 盎司'),
 '20619406':('HAVSTOBIS 浅绿色便当盒','透明 / 浅绿色 · 24 盎司'),
 '20625252':('IKEA 365+ 长方形玻璃保鲜盒','长方形 / 玻璃 · 34 盎司'),
 '00485101':('ELLOVEN 带抽屉显示器支架','深灰色 · 带收纳抽屉'),
 '10520892':('PLÖJA 白色透明桌垫','白色 / 透明 · 25½ × 17¾ 英寸'),
}
TITLES = [
 '普通的早晨，也值得用一只喜欢的杯子', '给早餐桌留一点点可爱 ☕', '白杯子这件事，我想简单一点', '白色杯子和一杯热茶，就是普通日子的浪漫',
 '不想开顶灯的晚上，留一盏温柔', '木头和暖光，是我理想中的下班时刻', '像一颗小月亮，给房间一点安静', '有一点复古的灯，和慢慢暗下来的天',
 '把沙发的这一角，留给不赶时间的周末', '房间不大，也要有能放松下来的地方', '秋天的居家愿望：柔软一点，再柔软一点', '想把阴天的蓝，轻轻放在沙发上',
 '一只不太规矩的花瓶，让角落活起来', '花不用很多，一两枝就很美', '买花之前，先给它准备一个小小的家', '棕红色的花器，像给房间加了一点秋天',
 '厨房的秩序感，藏在这些小盒子里', '明天的便当，今天就开始期待', '把冰箱收拾清爽，做饭也有了头绪', '给下午的小零食，找一个固定的位置',
 '桌面做减法，心情也跟着轻一点', '桌底那些电线，终于有了自己的位置', '给书桌铺一张干净的底色', '每天都用的桌面，值得认真整理一次',
]
COPIES = [
 '最近想把早餐桌收拾得简单一点：一只蓝杯子、一片面包，再留一点空白。\n\n看商品图时最喜欢的是这一抹不太张扬的蓝，配木桌或浅色桌布都很舒服。不是一定要买很多东西，有一件喜欢的，日常就有了一个小小的开始。',
 '有时候会被一点小图案打动。把这种轻松的颜色放在早餐桌上，连认真吃早饭这件事都变得有意思。\n\n想搭一只素色盘子，旁边放点水果。别把桌面摆满，给食物和自己都留一点空间。',
 '越看越喜欢没有太多装饰的白杯子。颜色不抢眼，换一张桌布、换一束花，整个桌面的感觉就不一样。\n\n我的灵感清单上写着：把每天都能用到的小东西先选好，其他的，慢慢来。',
 '白色杯子的干净，在于它可以和很多不同的日常放在一起。茶、咖啡，或者只是一杯温水，都有自己的时刻。\n\n想象一下周日早晨的光从窗边照进来，杯子旁边放着没读完的书——就已经很想好好待在家了。',
 '比起把整个房间照得很亮，更喜欢只照亮一个角落。奶白色的玻璃和圆圆的轮廓，视觉上就有一点柔和。\n\n准备把这个配色记进小家的灵感册里。要买灯的话，记得核对原页面的插头、电压和灯泡要求，这一件采集的是美规款。',
 '白蜡木加白色，刚好是那种不太费力的搭配。放在书旁边，或者一张小边桌上，画面已经很完整。\n\n我想要的下班时刻：手机远一点，茶近一点，房间只留一盏小灯。这里是公开商品资料的搭配灵感，实际规格以原页面为准。',
 '圆球形的小灯很像房间里的一颗月亮。没有复杂的线条，反而容易和不同的家具放在一起。\n\n想给晚上的阅读留一块温暖的位置。不过选灯不能只看图片，美规商品的电压和配件一定要先核对。',
 '有一点老电影里的感觉：黄铜色的底座，白色灯罩，旁边随意叠两本书。\n\n不想把房间装得太满，留这样一个小角落就很好。图片来自公开商品页，这里记录的是搭配想法。',
 '天气一转凉，就会想给沙发添一点柔软。白色的毯子和浅色抱枕放在一起，房间看起来安静了好多。\n\n想在周末窝在这里看一部片子，中途起来给自己煮杯茶。我的居家愿望其实一直很简单。',
 '最近的灵感词是“放松”。不是把家布置成展示间，而是给自己留一个坐下就不想着工作的地方。\n\n这条休闲毯的浅色很适合叠在沙发上。添一件东西前，先想想它会出现在生活的哪个时刻。',
 '米色的东西总有一种安稳感。配木色、白墙或者稍深一点的沙发，都不会显得太用力。\n\n已经在脑海里把客厅重新排了一遍：沙发上留一条毯子，茶几上少放几样东西，周末也就有了着落。',
 '不想让家里永远只有黑白灰。浅浅的蓝像一小块阴天，放在暖色的房间里，反而显得很轻。\n\n这组颜色先收进灵感册。慢慢找到自己的喜好，比一次把家装满更有意思。',
 '有些花瓶，即使没有花也很好看。这个弯弯的轮廓，让原本平直的书架多了一点变化。\n\n搭配上我会选细一点的枝条，把重点留给花器本身。生活里的小趣味，有时候真的就藏在这些不太规矩的线条里。',
 '白色花瓶的好处，是不会和花争抢颜色。一两枝有姿态的花，旁边留一点空白，比挤得满满当当更耐看。\n\n最近想试试把花摆在书桌侧边。工作间隙抬头看见一点自然的颜色，应该会很开心。',
 '小小的浅粉色，适合不想太隆重的日常。路过花店时不用纠结要不要买一大束，挑一两枝就够。\n\n想把它放在窗台，不刻意摆得整齐。让一点颜色出现在普通的一天里，就是我喜欢的仪式感。',
 '秋天的配色灵感：棕红、奶油白，再加一点木色。花器的颜色深一点，反而能把周围浅色的东西衬出来。\n\n不一定每次都摆鲜花，干枝、叶子或者空着，也有自己的样子。',
 '想要厨房看起来清爽，不一定是再添一个架子。先让零碎的东西各有位置，抽屉和台面就能松一口气。\n\n不锈钢的简洁感很适合这种思路。按商品页标注核对容量和使用限制，找到真正适合自己做饭习惯的那一只。',
 '给明天准备便当，某种程度上也是给自己一点期待。透明的盒子能一眼看到里面，浅绿色又有一点轻松。\n\n想装一份简单的饭和蔬菜，不追求摆得多精致，认真吃饭就很好。实际使用和加热方式记得看原商品说明。',
 '很喜欢打开冰箱时一目了然的感觉。把食材按自己的习惯分开，比照着别人的收纳图全部买一遍更实际。\n\n长方形的盒子容易排列，透明玻璃也方便看清内容。这是整理思路的灵感，密封、加热和清洗方式要以商品说明为准。',
 '坚果、饼干、切好的水果，给它们固定一个位置，下午想吃点什么的时候就不用翻来翻去。\n\n喜欢这种透明又简单的小盒子，不会让桌面显得太忙。把一点点秩序放进日常，真的很治愈。',
 '桌面最容易堆东西，所以一直想给小物件分一个固定的位置。抬高显示器的同时，多一个抽屉，思路就很清晰。\n\n深灰色适合和木色一起搭。动手整理之前，先量一下自己的设备尺寸和桌面空间，比照着图片直接买更稳妥。',
 '理想中的书桌，除了桌面清爽，下面也不该全是乱线。这个红色的小托盘让我想到：收纳不一定非要藏得一点颜色都没有。\n\n先把电源和线缆的位置想好，再安排其它东西。给插头留够空间，也别遮住需要散热的设备。',
 '给书桌留一片干净的工作区域，写字、看书时都能少一点分心。白色和透明的组合很轻，能让桌面原本的颜色透出来。\n\n不想把工具买得太多。先认真整理已经有的东西，再慢慢添适合自己的。',
 '经常拿取的小东西，比起藏进抽屉，更适合有一个顺手的落点。把笔、便签和零碎的小物件放在一起，桌面就不会到处都是。\n\n白色的小收纳件不抢眼，和木桌、书本都很好搭。这是我想继续尝试的桌面整理方向。',
]


def quote(value):
    return "'" + str(value).replace("'", "''") + "'"


def mysql(sql):
    command = ['docker','compose','exec','-T','mysql','sh','-c',
               'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --default-character-set=utf8mb4 -uroot -N -B "$MYSQL_DATABASE"']
    return subprocess.run(command, input=sql, text=True, capture_output=True, check=True, cwd=ROOT).stdout


def build(catalog):
    old = (ROOT / 'scripts' / 'cleanup_mock_data.sql').read_text()
    old = old.replace("username LIKE 'codexcommerce\\_%'", "(username LIKE 'codexcommerce%' OR username LIKE 'codexmock%' OR username REGEXP '^commerce_[0-9]+$' OR bio LIKE '%拾物演示人物%')")
    old = old[:old.index("SELECT 'remaining_seed_accounts'")]
    old = old.replace("name LIKE 'codexcommerce\\_%'", "(name LIKE 'codexcommerce%' OR name LIKE 'codexmock%')")
    old = old.replace('COMMIT;', '')
    extra = '''
DELETE FROM likes WHERE account_id IN (SELECT id FROM seed_accounts);
DELETE FROM likes WHERE video_id IN (SELECT id FROM videos WHERE author_id IN (SELECT id FROM seed_accounts));
DELETE FROM comments WHERE author_id IN (SELECT id FROM seed_accounts);
DELETE FROM comments WHERE video_id IN (SELECT id FROM videos WHERE author_id IN (SELECT id FROM seed_accounts));
DELETE FROM video_tags WHERE video_id IN (SELECT id FROM videos WHERE author_id IN (SELECT id FROM seed_accounts));
DELETE FROM outbox_msgs WHERE video_id IN (SELECT id FROM videos WHERE author_id IN (SELECT id FROM seed_accounts));
DELETE FROM videos WHERE author_id IN (SELECT id FROM seed_accounts);
DELETE FROM messages WHERE from_id IN (SELECT id FROM seed_accounts);
DELETE FROM messages WHERE to_id IN (SELECT id FROM seed_accounts);
DELETE FROM notifications WHERE recipient_id IN (SELECT id FROM seed_accounts);
DELETE FROM notifications WHERE sender_id IN (SELECT id FROM seed_accounts);
DELETE FROM outbox_events WHERE (aggregate_type IN ('post','content','content_post') AND aggregate_id IN (SELECT CAST(id AS CHAR) FROM seed_posts)) OR (aggregate_type IN ('order','payment') AND aggregate_id IN (SELECT CAST(id AS CHAR) FROM seed_orders));
DELETE FROM categories WHERE slug LIKE 'shiwugoods-%' AND id NOT IN (SELECT category_id FROM products WHERE category_id IS NOT NULL);
DELETE FROM tags WHERE (name LIKE 'codexmock%' OR name LIKE 'codexcommerce%' OR name IN ('postman','fault-drill')) AND id NOT IN (SELECT tag_id FROM post_tags);
DELETE FROM tags WHERE name IN ('杯壶日常','氛围灯光','温暖织物','花器摆件','厨房收纳','桌面好物') AND id NOT IN (SELECT tag_id FROM post_tags);
'''
    old = old.replace('DELETE FROM accounts WHERE', extra + '\nDELETE FROM accounts WHERE')
    lines = ["SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES,STRICT_TRANS_TABLES';", old, 'SET FOREIGN_KEY_CHECKS=0;']
    for i, (name, bio) in enumerate(PEOPLE):
        avatar = catalog['portraits'][i]['url']
        lines += ['INSERT INTO accounts (username,password,token,refresh_token,avatar_url,bio) VALUES (' + ','.join(map(quote,[name,PASSWORD_HASH,'','',avatar,bio+' 拾物演示人物'])) + ');', f'SET @u{i}=LAST_INSERT_ID();']
    shops = ['慢慢器物','一盏灯的时间','有织小屋','花与空瓶的店','厨房秩序','桌面编辑部']
    categories = list(dict.fromkeys(p['category'] for p in catalog['products']))
    for i, category in enumerate(categories):
        product = next(p for p in catalog['products'] if p['category'] == category)
        image = product['images'][1 if len(product['images'])>1 else 0]['url']
        lines += [f'INSERT INTO shops (owner_id,name,description,avatar_url,banner_url,status) VALUES (@u{i},'+','.join(map(quote,[shops[i],category+'精选 · 公开商品资料的本地体验小店，非品牌官方店铺。',catalog['portraits'][i]['url'],image,'open']))+');',f'SET @s{i}=LAST_INSERT_ID();',
                  'INSERT INTO categories (name,slug,status,sort_order) VALUES ('+','.join([quote(category),quote('shiwugoods-'+str(i)),quote('active'),str(i)])+');',f'SET @c{i}=LAST_INSERT_ID();',
                  'INSERT INTO tags (name) VALUES ('+quote(category)+');',f'SET @t{i}=LAST_INSERT_ID();']
    for i in range(12):
        lines.append(f'INSERT INTO socials (follower_id,vlogger_id) VALUES (@u12,@u{i});')
    review_index = 0
    for i, product in enumerate(catalog['products']):
        if product['code'] in LOCAL_NAMES and product['source_currency'] != 'CNY':
            product['name'],product['subtitle'] = LOCAL_NAMES[product['code']]
        if product['code'] == '80278367':
            # Source is the white ceramic variant, not the visually similar glass one.
            product['name'],product['subtitle'] = 'IKEA 365+ 白色马克杯','白色 · 12 盎司'
        category = categories.index(product['category'])
        author = category
        desc = f"{product['name']}\n\n{product['subtitle']}\n原商品款式：{product['source_name']}\n" + (f"原页标注材质：{product['material']}\n" if product['material'] else '') + f"\n{product['theme']}。图片、款式与资料来自 IKEA 公开商品页；本地库存仅供模拟购物。\n{product['source_url']}"
        price = product['price_cent']
        lines += [f'INSERT INTO products (shop_id,category_id,name,subtitle,description,cover_url,status,sales_count) VALUES (@s{category},@c{category},'+','.join(map(quote,[product['name'],product['subtitle'],desc,product['images'][0]['url'],'on_sale']))+',0);', f'SET @p{i}=LAST_INSERT_ID();']
        for index, image in enumerate(product['images']):
            lines.append(f'INSERT INTO product_images (product_id,storage_key,image_url,mime_type,byte_size,width,height,sort_order) VALUES (@p{i},'+','.join([quote('real-catalog/'+product['code']+'-'+str(index)),quote(image['url']),quote('image/jpeg'),str(image['byte_size']),'600','600',str(index)])+');')
        specs = {'款式': product['subtitle'] or product['source_name'].split(' - ')[-1]}
        lines += [f'INSERT INTO product_skus (product_id,sku_code,specs_json,price_cent,original_price_cent,available_stock,locked_stock,sold_stock,status) VALUES (@p{i},'+','.join([quote('REAL-'+product['code']),quote(json.dumps(specs,ensure_ascii=False)),str(price),str(price),str(40+i*3),'0','0',quote('active')])+');',f'SET @sku{i}=LAST_INSERT_ID();']
        lines += [f'INSERT INTO posts (author_id,title,body,status,likes_count,favorites_count,comments_count,popularity,published_at) VALUES (@u{author},'+','.join(map(quote,[TITLES[i],COPIES[i],'published']))+f',0,0,{len(product["reviews"])},0,UTC_TIMESTAMP(6)-INTERVAL {(i*7)%24+1} HOUR);',f'SET @post{i}=LAST_INSERT_ID();',f'INSERT INTO post_tags (post_id,tag_id) VALUES (@post{i},@t{category});',f'INSERT INTO post_products (post_id,product_id,sort_order) VALUES (@post{i},@p{i},0);']
        lifestyle = product['images'][1:] + product['images'][:1]
        for index, image in enumerate(lifestyle[:3]):
            lines.append(f'INSERT INTO post_images (owner_id,post_id,storage_key,image_url,mime_type,byte_size,width,height,sort_order,attached_at) VALUES (@u{author},@post{i},'+','.join([quote('real-catalog/post-'+product['code']+'-'+str(index)),quote(image['url']),quote('image/jpeg'),str(image['byte_size']),'600','600',str(index),'NOW(6)'])+');')
        for review in product['reviews']:
            username = f"{review['author'][:35]}·外站摘录{review_index}"
            review['username'] = username
            review['avatar_url'] = catalog['portraits'][len(PEOPLE)+review_index]['url']
            date = quote((review.get('date') or '2026-01-01')+' 12:00:00')
            lines += ['INSERT INTO accounts (username,password,token,refresh_token,avatar_url,bio) VALUES ('+','.join(map(quote,[username,PASSWORD_HASH,'','',review['avatar_url'],'IKEA 公开评价的引用身份。头像为示意照片，与原作者无关。拾物演示人物']))+');',f'SET @r{review_index}=LAST_INSERT_ID();',f'INSERT INTO post_comments (post_id,author_id,content,status,created_at,updated_at) VALUES (@post{i},@r{review_index},'+','.join([quote(review['text']),quote('active'),date,date])+');']
            review_index += 1
        count = 5+(i*5)%16
        for k in range(count):
            actor = (author+k+1)%24
            lines.append(f'INSERT INTO post_likes (post_id,account_id) VALUES (@post{i},@u{actor});')
            if k%3 == 0:
                lines.append(f'INSERT INTO post_favorites (post_id,account_id) VALUES (@post{i},@u{actor});')
        lines += [f'UPDATE posts SET likes_count={count},favorites_count={(count+2)//3},popularity={count*2+len(product["reviews"])*3} WHERE id=@post{i};']
    lines += ["INSERT INTO addresses (account_id,recipient_name,phone,province,city,district,detail,postal_code,is_default) VALUES (@u12,'小满（演示）','13800000000','浙江省','杭州市','西湖区','本地体验示例地址 1 号（非真实收货地址）','310000',TRUE);",
              'INSERT INTO cart_items (account_id,sku_id,quantity,observed_price_cent) VALUES (@u12,@sku0,1,'+str(catalog['products'][0]['price_cent'])+'),(@u12,@sku8,1,'+str(catalog['products'][8]['price_cent'])+');',
              'SET FOREIGN_KEY_CHECKS=1;', 'COMMIT;']
    return '\n'.join(lines)


def publish(catalog):
    sql = '''SELECT JSON_OBJECT('id',id,'username',username,'avatar_url',avatar_url) FROM accounts WHERE bio LIKE '%拾物演示人物%';
SELECT JSON_OBJECT('id',p.id,'code',SUBSTRING(s.sku_code,6),'sku_id',s.id) FROM products p JOIN product_skus s ON s.product_id=p.id WHERE s.sku_code LIKE 'REAL-%';
SELECT JSON_OBJECT('comment_id',c.id,'author_id',c.author_id,'username',a.username) FROM post_comments c JOIN accounts a ON a.id=c.author_id WHERE a.bio LIKE '%拾物演示人物%';
SELECT JSON_OBJECT('post_id',pp.post_id,'product_id',pp.product_id) FROM post_products pp JOIN product_skus s ON s.product_id=pp.product_id WHERE s.sku_code LIKE 'REAL-%';'''
    records = [json.loads(line) for line in mysql(sql).splitlines() if line.startswith('{')]
    people = [x for x in records if 'avatar_url' in x]
    product_ids = {x['code']:x for x in records if 'code' in x}
    comments = {x['username']:x for x in records if 'comment_id' in x}
    for p in catalog['products']:
        p.update(product_ids[p['code']])
        for review in p['reviews']:
            record = comments[review['username']]
            review.update(id=record['comment_id'],author_id=record['author_id'])
    result = {'collected_at':catalog['collected_at'],'notice':catalog['notice'],'products':catalog['products'],'people':people,'posts':[x for x in records if 'post_id' in x]}
    PUBLIC.mkdir(parents=True,exist_ok=True)
    (PUBLIC/'provenance.json').write_text(json.dumps(result,ensure_ascii=False,indent=2))
    rows=''.join(f'<tr><td>{html.escape(p["name"])}</td><td>{p["source_currency"]} {p["source_price"]}</td><td><a href="{html.escape(p["source_url"])}">原商品</a> · <a href="{html.escape(p["review_source_url"])}">评价来源</a></td></tr>' for p in catalog['products'])
    document='''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>拾物 · 数据来源</title><style>body{max-width:960px;margin:60px auto;padding:0 24px;font:15px/1.9 system-ui;color:#54483d;background:#faf9f7}h1{font-size:30px;color:#302720}a{color:#e25660}table{width:100%;border-collapse:collapse;font-size:12px}td,th{text-align:left;padding:14px;border-bottom:1px solid #e9e0d6}.note{background:#fff2e8;padding:20px;border-radius:14px}small{color:#a28e7b}</style><a href="/">← 回到拾物</a><h1>好物有出处，评价有来源。</h1><p class="note">这是本地购物体验站，非 IKEA 官方网站或授权销售渠道。商品资料与图片来自下列公开商品页，短评保留原文和原作者，原商品的外站评分与评价数不代表本站成交。头像为 Random User 示例照片，与原评价作者无关。人物主页、灵感笔记、关注、点赞、库存和收货地址为演示内容；笔记非真实购买测评。中国官网价格按采集结果保留；仅有美国官网价格的商品，本站人民币演示价按美元价格乘 7 设置，不代表实时汇率或真实零售价。</p>'''+f'<p>采集时间：{html.escape(catalog["collected_at"])} · {len(catalog["products"])} 件商品 · {sum(len(p["reviews"]) for p in catalog["products"])} 条公开评价短摘录。</p><table><thead><tr><th>商品</th><th>原页价格</th><th>来源</th></tr></thead><tbody>'+rows+'</tbody></table><p><a href="https://randomuser.me/">示意头像来源：Random User</a></p><small>商品图片权利归原权利人所有。采集资料用于本地开发展示；公开发布或商业使用前应核对对应许可和平台条款。每个商品页引用的评价原文总量不超过 25 个英文单词。</small></html>'
    (PUBLIC/'sources.html').write_text(document)
    (DATA/'imported.json').write_text(json.dumps(result,ensure_ascii=False,indent=2))
    print(f'Imported {len(catalog["products"])} products, {len(comments)} sourced reviews, {len(people)} demo/reference profiles.')


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--apply',action='store_true');args=parser.parse_args()
    catalog=json.loads((DATA/'catalog.json').read_text())
    assert len(catalog['products'])==24
    for p in catalog['products']:
        assert p['images'] and p['reviews']
        for image in p['images']:
            assert (ROOT/'frontend'/'public'/image['url'].lstrip('/')).exists()
        assert sum(len(r['text'].split()) for r in p['reviews'])<=25
    sql=build(catalog)
    (DATA/'import.sql').write_text(sql)
    if args.apply:
        try: mysql(sql)
        except subprocess.CalledProcessError as e: raise RuntimeError(e.stderr) from e
        publish(catalog)
    else: print('Prepared', DATA/'import.sql', 'Use --apply after backup and stopping writers.')

if __name__=='__main__':main()
