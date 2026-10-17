#!/usr/bin/env python3
"""Collect public IKEA product facts, images and short attributed review excerpts.

Only requests public HTML. No login, browser cookies or private APIs are used.
Downloaded portraits are illustrative photos from Random User, never reviewer photos.
"""
import concurrent.futures
import datetime
import html
import json
from pathlib import Path
import re
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / 'data' / 'real-catalog'
ASSETS = ROOT / 'frontend' / 'public' / 'media' / 'catalog'
HEADERS = {'User-Agent': 'FeedSystem-LocalDemo/1.0 (public product reference collector)'}
CATEGORIES = [
    ('杯壶日常', 'mugs-cups-16045', '给每天留一杯的时间'),
    ('氛围灯光', 'table-lamps-10732', '把普通的夜晚照亮'),
    ('温暖织物', 'blankets-throws-20528', '家的柔软时刻'),
    ('花器摆件', 'vases-10776', '把喜欢的花带回家'),
    ('厨房收纳', 'food-containers-20606', '厨房也有自己的秩序'),
    ('桌面好物', 'desk-accessories-10573', '整理一张舒服的书桌'),
]


def fetch(url, filename=None):
    if filename and filename.exists():
        return filename.read_bytes()
    last_error = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers=HEADERS), timeout=35) as response:
                content = response.read()
            if filename:
                filename.parent.mkdir(parents=True, exist_ok=True)
                filename.write_bytes(content)
            return content
        except (OSError, urllib.error.HTTPError) as error:
            last_error = error
            time.sleep(attempt + 1)
    raise last_error


def product_json(page):
    for raw in re.findall(r'<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>', page, re.S):
        value = json.loads(raw)
        if isinstance(value, dict) and value.get('@type') == 'Product':
            return value
    raise ValueError('No public Product structured data')


def chinese_product(page):
    raw = re.search(r'<script[^>]*id="__NUXT_DATA__"[^>]*>(.*?)</script>', page, re.S)
    if not raw:
        return None
    data = json.loads(raw.group(1))
    cache = {}

    def resolve(index):
        if index < 0:
            return None
        if index in cache:
            return cache[index]
        value = data[index]
        if isinstance(value, dict):
            cache[index] = {}
            cache[index].update({key: resolve(item) for key, item in value.items()})
        elif isinstance(value, list):
            if value and isinstance(value[0], str):
                cache[index] = resolve(value[1]) if len(value) == 2 else None
            else:
                cache[index] = [resolve(item) for item in value]
        else:
            cache[index] = value
        return cache[index]

    for index, value in enumerate(data):
        if isinstance(value, dict) and all(key in value for key in ['productId', 'benefits', 'images', 'price']):
            return resolve(index)
    return None


def collect_one(entry):
    url, category, theme = entry
    code = re.search(r'([0-9]{8})/$', url).group(1)
    page = fetch(url, OUTPUT / 'pages' / (code + '-us.html')).decode()
    product = product_json(page)
    if not product.get('image') or not product.get('offers', {}).get('price'):
        return None
    cn_url = url.replace('www.ikea.com/us/en/', 'www.ikea.cn/cn/zh/')
    cn = None
    try:
        cn = chinese_product(fetch(cn_url, OUTPUT / 'pages' / (code + '-cn.html')).decode())
    except Exception:
        pass
    reviews = []
    # At most 25 verbatim words are retained from each product page.
    budget = 25
    candidates = sorted(product.get('review', []), key=lambda item: len(item.get('reviewBody', '').split()))
    for review in candidates:
        body = html.unescape(review.get('reviewBody', '')).strip()
        count = len(body.split())
        if not body or count > budget or len(reviews) >= 3:
            continue
        budget -= count
        reviews.append({'author': review['author']['name'], 'text': body,
                        'rating': review['reviewRating']['ratingValue'],
                        'date': review.get('datePublished'), 'source_url': url})
    if not reviews:
        return None
    image_sources = [item['contentUrl'] if isinstance(item, dict) else item for item in product['image']][:4]
    images = []
    for index, source in enumerate(image_sources):
        target = ASSETS / (code + '-' + str(index) + '.jpg')
        fetch(source + '?f=m', target)
        images.append({'url': '/media/catalog/' + target.name, 'source_url': source, 'byte_size': target.stat().st_size})
    price = cn.get('price', {}).get('regularPrice') if cn else None
    if isinstance(price, dict):
        price = price.get('value')
    try:
        price = float(price)
    except (TypeError, ValueError):
        price = None
    result = {
        'code': code, 'category': category, 'theme': theme,
        'name': ((cn.get('name', '') + ' ' + cn.get('type', '')) if cn else product['name']).strip(),
        'subtitle': (cn.get('designText', '') + ' · ' + cn.get('measureText', '')).strip(' ·') if cn else product.get('color', ''),
        'source_name': product['name'], 'material': product.get('material', ''),
        'source_url': cn_url if cn else url, 'review_source_url': url,
        'price_cent': round(price * 100) if price is not None else round(float(product['offers']['price']) * 100 * 7),
        'source_price': price if price is not None else float(product['offers']['price']),
        'source_currency': 'CNY' if price is not None else 'USD',
        'price_note': '中国官网采集价' if price is not None else '演示人民币价格，按美元采集价 × 7 设置，并非实时汇率',
        'rating': float(product.get('aggregateRating', {}).get('ratingValue', 0)),
        'review_count': int(product.get('aggregateRating', {}).get('reviewCount', 0)),
        'images': images, 'reviews': reviews,
    }
    print('Collected', code, result['name'], len(images), 'images', len(reviews), 'reviews', flush=True)
    return result


def enrich_chinese_prices(products):
    """Match only exact global article IDs, never visually similar variants."""
    index = {}
    for _, slug, _ in CATEGORIES:
        try:
            page = fetch('https://www.ikea.cn/cn/zh/cat/' + slug + '/', OUTPUT / 'pages' / ('cn-' + slug + '.html')).decode()
            raw = re.search(r'<script[^>]*id="__NUXT_DATA__"[^>]*>(.*?)</script>', page, re.S)
            values = json.loads(raw.group(1))
            for value in values:
                if isinstance(value, dict) and all(key in value for key in ['globalId', 'id', 'price', 'name']):
                    index[values[value['globalId']]] = values[value['id']]
        except Exception as error:
            print('Chinese category unavailable', slug, str(error), flush=True)
    for product in products:
        local_id = index.get(product['code'])
        if not local_id:
            continue
        url = 'https://www.ikea.cn/cn/zh/p/product-' + local_id + '/'
        try:
            cn = chinese_product(fetch(url, OUTPUT / 'pages' / (local_id + '-cn.html')).decode())
            if not cn or cn['globalId'] != product['code']:
                continue
            price = float(cn['price']['regularPrice'])
            product.update(name=cn['name'] + ' ' + cn['type'],
                           subtitle=(cn.get('designText', '') + ' · ' + cn.get('measureText', '')).strip(' ·'),
                           source_url=url, source_currency='CNY', source_price=price,
                           price_cent=round(price * 100), price_note='中国官网采集价')
        except Exception as error:
            print('Chinese product unavailable', local_id, str(error), flush=True)


def main():
    OUTPUT.mkdir(parents=True, exist_ok=True)
    ASSETS.mkdir(parents=True, exist_ok=True)
    entries = []
    seen = set()
    for category, slug, theme in CATEGORIES:
        url = 'https://www.ikea.com/us/en/cat/' + slug + '/'
        page = fetch(url, OUTPUT / 'pages' / (slug + '.html')).decode()
        paths = list(dict.fromkeys(re.findall(r'(?:https://www\.ikea\.com)?(/us/en/p/[a-z0-9-]+-[0-9]{8}/)', page)))
        for path in paths:
            code = re.search(r'([0-9]{8})/$', path).group(1)
            if code not in seen:
                entries.append(('https://www.ikea.com' + path, category, theme))
                seen.add(code)
            if len([entry for entry in entries if entry[1] == category]) >= 6:
                break
        print('Category', category, len(paths), 'public product links', flush=True)
    products = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        futures = {pool.submit(collect_one, entry): entry for entry in entries}
        for future in concurrent.futures.as_completed(futures):
            try:
                value = future.result()
                if value:
                    products.append(value)
            except Exception as error:
                print('Skipped', futures[future][0], str(error), flush=True)
    selected = []
    for category, _, _ in CATEGORIES:
        selected.extend(sorted([p for p in products if p['category'] == category], key=lambda p: p['code'])[:4])
    enrich_chinese_prices(selected)
    portraits = []
    for index in range(91):
        gender = 'women' if index % 2 == 0 else 'men'
        used_numbers = [44, 32, 68, 46, 49, 12, 65, 33, 24, 76, 79, 51, 21, 15, 5, 22]
        extra_numbers = [value for value in range(100) if value not in used_numbers]
        number = used_numbers[index // 2] if index < 32 else extra_numbers[(index - 32) // 2]
        url = f'https://randomuser.me/api/portraits/{gender}/{number}.jpg'
        target = ASSETS / f'portrait-{index}.jpg'
        fetch(url, target)
        portraits.append({'url': '/media/catalog/' + target.name, 'source_url': url, 'illustrative': True})
    manifest = {'collected_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                'notice': '商品资料与短评摘自公开页面；头像为示意照片，与原评价作者无关；笔记为编辑创作，非购买证明。',
                'products': selected, 'portraits': portraits}
    (OUTPUT / 'catalog.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2))
    print('READY', len(selected), 'products', sum(len(p['reviews']) for p in selected), 'attributed reviews', flush=True)


if __name__ == '__main__':
    main()
