# Lemon Search: 4-Day Build Trial

# **Lemon Search: 4-Day Build Trial**

You are building the search and ranking system for Lemon. The engine that takes a query and returns the right local businesses, ranked well. Real backend, real data, deployed live. This is a slice of the actual first thing you'd own if you join. Read the whole spec before writing code.

Respond in the thread you received this in with any questions.

What this tests. Search sounds simple but is hard to do well. Typo tolerance, ranking quality, relevance, speed. We are grading how good the search actually is, how you architected it, and the judgment in what you built versus cut.

---

## **What to build**

A working search system over real Miami business data: a search bar, live results as the user types, top 15 ranked businesses per query. One retrieval and ranking core.

---

### **Data**

We give you Lemon's full [business database](https://drive.google.com/drive/folders/1oTQl59sAhU_7qiXTLtmISuTRjyvN6PjU?usp=drive_link), real Miami businesses with name, category, subcategory, sub-sub-category, neighborhood, address, lat/lng, photos, structured hours, price tier, phone, reaction score (0-10), and reaction count.

Your job is to index it and build search on top. How you structure and index the data is graded. If you spot data-quality issues (SEO-spam names, bad categories, broken hours), flag them in your writeup.

---

## **Retrieval requirements**

Pick your search engine, Algolia, Typesense, Meilisearch, or Postgres full-text. Or any other ones. The choice is yours; justify it in the writeup based on what makes sense at V1 scale.

* **Typo tolerance.** 1-4 character tolerance per word. `joes barbr shop` finds `Joe's Barber Shop`. `steaak` finds steak. `gym with poo` finds gym with pool.  
* **Partial and prefix matching.** `best steakh` surfaces `best steakhouse`.  
* **Exact name boost.** Typing a business name returns that business first, regardless of other ranking signals.  
* **Category-aware matching.** A query matching a category returns businesses of that category.  
* **Smart semantic search**. The system should handle simple natural language intent queries and map them to relevant business attributes or categories. Queries like “cheap restaurants” should prioritize affordable restaurants using price tier signals, while queries like “i’m hungry” can intelligently surface nearby open restaurants, cafés, or fast food spots. This does not need full LLM-based retrieval, but should demonstrate lightweight intent understanding beyond strict keyword matching.  
* **Speed.** Sub-100ms response on every keystroke, p95.

---

## **Ranking**

Every result is scored by 7 signals, each normalized 0-1, multiplied by an archetype weight, summed for a final score.

**The 7 signals:**

1. **Distance** \- inverse distance from a fixed user location (pick a Miami lat/lng), capped at 30 miles. Closer is higher.  
2. **Rating** \- reaction score / 10\.  
3. **Reaction count** \- log-scaled confidence. 800 reactions should not bury 50, but more reactions means more confidence.  
4. **Friend signal** \- synthesize a small friends-reacted dataset. Any friend reacted positively boosts; more friends, bigger boost.  
5. **Claimed status** \- synthesize a claimed/unclaimed flag. Claimed gets a big boost, unclaimed gets none.  
6. **Photo count** \- 3+ photos full eligibility, under 3 a significant demotion.  
7. **Open status** \- open now beats opens-later beats closed, computed from the structured hours and a fixed current time.

**Category archetypes**. Every category maps to one of six archetypes. Archetype weights are hardcoded but live in a config file, so tuning needs no code changes.

* **Low-stakes, fast & nearby** (restaurants, cafés, bars, fast food, car detailing, farmers markets, ethnic/organic grocery, butcher): heavy on distance, keyword match, rating × volume, friends, photo quality; open status is a hard filter.  
* **Medium-stakes, occasion** (gyms, salons, barbers, nail salons, spas, tattoo, studios, personal training, wellness, car repair, vets, pet boarding/training): heavy on rating × volume, friends, tag affinity, photo quality; distance moderate; open status soft.  
* **High-stakes, one-time** (all home improvement, moving, weddings, event venues, catering, photography, DJs, event planning, florists, rentals): heavy on rating × volume, friends, claimed status, photo quality, reaction recency; service area as filter, not distance score.  
* **Experiential / aspirational** (boats, beach clubs, theaters, hotels, destination spas, all activities and experiences): heavy on photo quality, rating × volume, friends, keyword match; distance and onboarding categories moderate; open status near-irrelevant.  
* **Recurring service** (cleaners, dog walkers, meal prep, errands, pet grooming/walking, coworking): heavy on rating × volume, friends, repeat-booking rate, vendor reliability, claimed status; distance as service-area filter for home services.  
* **Utility / distance-dominant** (towing, tires, glass repair, supermarket, convenience store, meeting rooms, day passes): distance extremely high; open status hard filter; rating as quality floor; social and visual signals minimal.

**New businesses** (under 10 reactions): show with a "new" badge, slight rating-signal demote, don't surface at the very top.

---

## **Cross-cutting requirements**

* **Search has to be genuinely good.** This is the whole point. Fast, forgiving, smart. Bad ranking or weak typo tolerance is a fail regardless of how clean the code is.  
* **No visible bugs.** Search runs clean \- no broken states, no flicker, no console errors a user would notice. Bug fixing is a primary priority, not a final-day cleanup. If something is broken and you couldn't fix it, say so in the writeup; an unflagged bug we find ourselves is worse than one you flagged.  
* **A thin, functional UI.** Build just enough interface to demonstrate search working \- a search bar with live results. It does not need to be designed or polished. We are grading the engine, not the visuals. Plain and working beats pretty and broken.

---

## **Deliverables**

1. A live deployed URL on Vercel or Netlify.  
2. A GitHub repo with full commit history. Daily commits are expected and we will review them.  
3. The backend project (Supabase), with read access shared so we can inspect schema and data.  
4. A full writeup covering, in depth: search engine choice and why; schema decisions and trade-offs; how ranking and archetypes are implemented; spec ambiguities you hit and the calls you made; what you would change with another week; what is broken and what you would fix first.

---

## **Stack and tools**

Next.js, Supabase (Postgres, the stack Lemon runs on). Vercel or Netlify to deploy. Cursor, Claude Code, Codex, and Copilot are encouraged \- use them aggressively.

**Not allowed:** Lovable, Bolt, rork, Replit, v0, or any vibe-coding platform. We want to see how you build.

---

## **Grading**

1. **Search quality.** Does it return the right businesses, fast, with real typo tolerance and exact-name boost. The core signal.  
2. **Ranking quality.** Are the 7 signals and 4 archetypes implemented correctly and do the results feel right.  
3. **Backend quality.** Schema, retrieval architecture, data modeling, the scrape.  
4. **Speed.** Sub-100ms p95, and whether you treated it as a real constraint.  
5. **No bugs.** Does the whole thing run clean?  
6. **Code quality.** Readable, sensibly architected.  
7. **The writeup.** Judgment is the biggest signal- what you kept, cut, and why.  
8. **Communication.** Daily progress updates without us asking is a strong positive signal.

---

## **Appendix: V1 product context**

For context only \- not part of the deliverable. The full search system eventually powers four surfaces (search bar, category browse, AI natural-language search, recommended feed), a per-user learning loop, and logging infrastructure. You are building the retrieval-and-ranking core plus the search bar. Build it so the rest could plug in later.

# Taxonomoy

## **Taxonomy**

| Category | Sub Category | Preference / Sub-Sub |
| ----- | ----- | ----- |
| Food & Drinks | Restaurant | American, Mexican, Italian, Japanese, Chinese, Thai, Indian, Greek, Seafood, Steakhouse, Breakfast/Brunch, Vegan/Vegetarian |
| Food & Drinks | Casual / Fast | Burgers, Pizza, Tacos, Sushi, Poke, Chicken, Sandwiches, Food Truck, Bowls, Desserts |
| Food & Drinks | Bar | Sports Bar, Wine Bar, Cocktail Bar, Rooftop Bar, Brewery/Taproom, Lounge, Pub, Nightclub, Hookah Bar |
| Food & Drinks | Café | Coffee Shop, Tea House, Bakery, Juice/Smoothie Bar, Dessert Café, Bubble Tea |
| Food & Drinks | Catering | Event Catering, Meal Prep, Private Chef |
| Beauty | Hair Salon | Women's, Men's, Unisex, Braids/Natural Hair, Color Specialist, Extensions |
| Beauty | Barbershop | \- |
| Beauty | Nail Salon | Basic Nail Salon, Nail Art Studio |
| Beauty | Spa | Day Spa, Med Spa, Massage, Facial, Couples Spa, Float Therapy / Cryotherapy |
| Beauty | Skincare | Esthetician, Laser, Microneedling, Chemical Peel |
| Beauty | Makeup & Lashes | Makeup Artist, Microblading, Lash Extensions, Brow Studio |
| Beauty | Tattoo & Piercing | \- |
| Fitness & Wellness | Gym | Commercial, Boutique, CrossFit, Powerlifting, 24-Hour, Women-Only, Climbing Gym, Pool / Aquatic Center |
| Fitness & Wellness | Studio | Yoga, Pilates, Cycling/Spin, Dance, Boxing/Kickboxing, HIIT, Martial Arts |
| Fitness & Wellness | Personal Training | In-Person, Online, Group |
| Fitness & Wellness | Wellness | Chiropractic, Acupuncture, Cryotherapy, Float Therapy, IV Therapy, Meditation |
| Home Improvement | Contractor | Kitchen, Bathroom, Whole Home, Addition |
| Home Improvement | Outdoor & Garden | Landscaping, Pool Service, Lawn Care, Pest Control |
| Home Improvement | Electrician | \- |
| Home Improvement | Plumber | \- |
| Home Improvement | HVAC | \- |
| Home Improvement | Painting | Interior, Exterior |
| Home Improvement | Flooring | \- |
| Home Improvement | Roofing | \- |
| Home Improvement | Interior Design | \- |
| Home Improvement | Carpentry | Cabinets, Deck Building, Fence Building |
| Home Improvement | Windows & Doors | Windows, Doors |
| Home Improvement | Masonry & Concrete | \- |
| Home Improvement | Handyman | \- |
| Time Savers | Cleaning | House Cleaning, Deep Clean, Move-In/Move-Out |
| Time Savers | Moving | Local, Long Distance, Packing, Junk Removal |
| Time Savers | Laundry | Wash & Fold, Dry Cleaning, Alterations |
| Time Savers | Errands | Grocery Delivery, Personal Errands, Courier |
| Time Savers | Organization | Home Organizing, Closet Systems |
| Time Savers | Private Chef | \- |
| Pets | Grooming | Dog, Cat, Mobile |
| Pets | Vet | General, Emergency |
| Pets | Boarding & Sitting | Boarding, Daycare, In-Home Sitting |
| Pets | Training | \- |
| Pets | Walking | \- |
| Events | Weddings | Planner, Venue, Florist, DJ/Music, Photography, Catering, Rentals |
| Events | Venue | Banquet Hall, Outdoor, Rooftop, Beach |
| Events | Photography & Video | Event, Drone |
| Events | Catering | \- |
| Events | DJ / Music | DJ, Live Band, Solo Musician |
| Events | Event Planning | Day-Of Coordinator, Event Planner |
| Events | Florist | \- |
| Events | Rentals | Tables/Chairs, Tent, Lighting, Decor |
| Car | Repair | General Mechanic, Brakes, Transmission, Engine, Oil Change |
| Car | Detailing | Full Detail, Ceramic Coating, Mobile Detailing |
| Car | Body & Paint | Collision Repair, Dent Removal, Wrap/Vinyl |
| Car | Tires | Tire Shop, Alignment |
| Car | Towing & Roadside | \- |
| Car | Glass Repair | \- |
| Activities & Experiences | Bowling | \- |
| Activities & Experiences | Golf | Golf Course, Driving Range, Mini Golf |
| Activities & Experiences | Racquet Sports | Padel, Tennis |
| Activities & Experiences | Action Sports | Paintball, Airsoft, Laser Tag, Axe Throwing |
| Activities & Experiences | Arcades & Games | Arcade, Escape Room, Go-Karts, Trampoline Park |
| Activities & Experiences | Water Sports | Kayak / Paddleboard, Boat Tour, Fishing Charter, Jet Ski, Scuba / Snorkel, Parasailing |
| Activities & Experiences | Arts & Culture | Museum, Art Gallery, Movie Theater, Comedy Club, Zoo / Aquarium, Art Class |
| Activities & Experiences | Parks & Nature | \- |
| Activities & Experiences | Boat Tour | \- |
| Activities & Experiences | Food Tour | \- |
| Activities & Experiences | Cooking Class | \- |
| Co-working | Hot Desk | \- |
| Co-working | Private Office | \- |
| Co-working | Meeting Room | \- |
| Co-working | Day Pass | \- |
| Co-working | Virtual Office | \- |
| Grocery | Supermarket | \- |
| Grocery | Organic / Health Food | \- |
| Grocery | International / Ethnic | \- |
| Grocery | Butcher / Seafood | \- |
| Grocery | Farmers Market | \- |
| Grocery | Convenience Store | \- |

