---
name: job-description-analyzer
description: Reference for job-fit and resume-tailor: extracts requirements, must-haves and nice-to-haves from a posting, maps them to the profile and lists the gaps. Use when the user pastes a posting and asks "what do they want", "am I a match", or "what am I missing"; scores are recorded by job-fit.
license: MIT
metadata:
  source: https://github.com/Paramchoudhary/ResumeSkills
  vendored-by: scoutjobs
---

# Job Description Analyzer

## When to Use This Skill

Use this skill when the user:
- Wants to analyze a job posting
- Asks "should I apply to this job?"
- Wants to know their match percentage for a role
- Needs help understanding job requirements
- Wants to tailor their resume for a specific position
- Mentions: "analyze this job", "am I qualified", "match score", "should I apply"

Use this BEFORE resume tailoring to ensure effort is worth it.

## Core Capabilities

- Extract and categorize job requirements (must-have vs nice-to-have)
- Calculate match score between user's experience and job requirements
- Identify skill gaps and strengths
- Detect red flags in job postings
- Prioritize which experiences to highlight
- Generate resume tailoring strategy
- Create cover letter talking points
- Assess company culture fit indicators

## The Strategic Problem

Most job seekers waste time on:
- Jobs they're under-qualified for (<60% match)
- Jobs they're over-qualified for (flight risk)
- Jobs with red flags (high turnover, toxic culture)
- Applying to 50+ jobs blindly hoping something sticks

Better approach:
- Apply to 10-15 jobs strategically
- Target 70-90% match jobs
- Customize deeply for each
- Higher response rate, less burnout

## Analysis Process

### Step 1: Extract Requirements

Break job description into categories:

**Required (Must-Have)**
- Education requirements
- Years of experience
- Specific technical skills
- Certifications/licenses
- Industry experience

**Preferred (Nice-to-Have)**
- "Bonus" skills
- Advanced certifications
- Domain expertise
- Specific tool experience

**Soft Skills/Culture**
- Communication style
- Work environment
- Team structure
- Company values

### Step 2: Keyword Extraction

Identify three types:

**Hard Skills** (Technical abilities)
- Tools: Salesforce, Python, AWS, Excel
- Methodologies: Agile, Six Sigma, SDLC
- Certifications: PMP, CPA, AWS Certified

**Soft Skills** (Interpersonal)
- Leadership, collaboration, communication
- Problem-solving, critical thinking
- Adaptability, initiative

**Industry/Domain Knowledge**
- B2B SaaS, healthcare, fintech
- Enterprise vs SMB
- Regulatory knowledge (HIPAA, SOX, GDPR)

### Step 3: Calculate Match Score

```
MATCH CALCULATION:

Required Skills:
- User has 8 out of 10 required = 80%

Preferred Skills:
- User has 3 out of 5 preferred = 60%

Overall Match:
- Weight required 70%, preferred 30%
- (80% × 0.7) + (60% × 0.3) = 74%

INTERPRETATION:
90-100% = Overqualified (may be flight risk)
75-89% = Excellent fit (apply immediately)
60-74% = Good fit (apply with strong cover letter)
50-59% = Stretch role (apply if passionate)
<50% = Under-qualified (skip unless dream job)
```

### Step 4: Gap Analysis

For each missing requirement:
- **Critical gap**: Deal-breaker (don't apply)
- **Major gap**: Significant but addressable (mention in cover letter)
- **Minor gap**: Easy to learn (downplay or emphasize related skills)

### Step 5: Red Flag Detection

Scan for warning signs:

**Workload Red Flags:**
- "Wear many hats"
- "Fast-paced environment"
- "Hit the ground running"
- "Self-starter in ambiguous situations"

**Culture Red Flags:**
- "Rockstar/Ninja/Guru"
- "We work hard, play hard"
- "Unlimited vacation"
- "Like a family"

**Compensation Red Flags:**
- "Competitive salary" (won't tell you range)
- "Equity-heavy" (low cash compensation)
- "Commission-based" (no base salary)
- "DOE" with no range

## Match Score Output Format

A full worked report (score, requirement breakdown, strengths, gaps, resume strategy, cover letter points, red flags, timeline, decision) is in `report-example.md` next to this file. Read it only when the user asks for the full written report; the ScoutJobs flow below needs just the score and gaps.

## Requirement Classification Guide

### Identifying "Must Have" vs "Nice to Have"

**Language indicating REQUIRED:**
- "Must have..."
- "Required: X years of..."
- "You have..."
- "Essential qualifications"
- Listed under "Requirements"
- Mentioned 3+ times in description

**Language indicating PREFERRED:**
- "Nice to have..."
- "Bonus if you have..."
- "Preferred qualifications"
- "Ideally, you'd have..."
- "A plus if..."
- Mentioned only 1-2 times

### Dealbreaker Detection

**Absolute dealbreakers (don't apply):**
- Required license you don't have (medical, legal, CPA)
- Required clearance you can't get
- Years of experience 50%+ below requirement
- Required degree you don't have (when stated as "required")
- Location requirement you can't meet

**Not dealbreakers (apply anyway):**
- Years of experience slightly below (e.g., 3 years when they want 5)
- "Preferred" degree you don't have
- Nice-to-have tools/skills you can learn
- Industry experience when you have transferable skills

## Implementation Checklist

When analyzing a job:

1. ✅ Extract all requirements (required vs preferred)
2. ✅ Identify all keywords (hard skills, soft skills, industry terms)
3. ✅ Calculate match score
4. ✅ Identify strengths to emphasize
5. ✅ Identify gaps and strategies to address
6. ✅ Detect red flags
7. ✅ Create resume customization plan
8. ✅ Generate cover letter talking points
9. ✅ Research company
10. ✅ Provide application recommendation and timeline

## Edge Cases

### Vague Job Descriptions
- Flag as potential red flag
- Extract what keywords you can
- Recommend reaching out for clarity before applying
- Use industry standard requirements as baseline

### Multiple Roles in One JD
- Identify the core role vs "other duties"
- Focus match score on primary responsibilities
- Flag scope creep concerns

### Internal Postings (Already Working There)
- Different strategy - emphasize internal knowledge
- Highlight cross-team relationships
- Reference specific company initiatives

### Reposted Jobs
- May indicate: Previous hire didn't work out, role expanded, or first search failed
- Worth applying, but research why it was reposted
- Check if requirements changed from original posting


## ScoutJobs integration

Save the analysis as `analysis.md` in the application folder and summarize must-haves into `scoutjobs job set <id> --tags "<comma list>"`. The `job-fit` skill uses this analysis when present.
