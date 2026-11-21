# Independently authored observer of Django 6.1 BaseModelFormSet persistence.
# Reference commit fe0a859f537d4238cf49fca39073513206f83122; BSD-3-Clause,
# see LICENSE.django. No Django implementation code is translated here.
from pathlib import Path
import hashlib, inspect, json, platform, sys, tempfile
import django
from django.conf import settings

assert django.get_version() == '6.1' and platform.python_version() == '3.14.3'
with tempfile.TemporaryDirectory(prefix='godj-native-set-save-') as directory:
    root = Path(directory)
    app = root / 'setsave'
    app.mkdir()
    (app / '__init__.py').write_text('')
    (app / 'models.py').write_text('''from django.db import models
class Label(models.Model):
    code = models.CharField(max_length=12)
class Article(models.Model):
    title = models.CharField(max_length=24)
    hidden = models.CharField(max_length=24, default="initial")
    labels = models.ManyToManyField(Label, blank=True, related_name="labeled_articles")
    reviewers = models.ManyToManyField(Label, blank=True, related_name="reviewed_articles")
''')
    sys.path.insert(0, directory)
    settings.configure(SECRET_KEY='synthetic-set-save', INSTALLED_APPS=['setsave'], USE_I18N=False,
                       DATABASES={'default': {'ENGINE': 'django.db.backends.sqlite3', 'NAME': ':memory:'}})
    django.setup()
    from django import forms
    from django.db import connection, transaction
    from django.forms import models as form_models
    from django.db.models.signals import m2m_changed
    from django.test.utils import CaptureQueriesContext
    from setsave.models import Article, Label
    with connection.schema_editor() as editor:
        editor.create_model(Label)
        editor.create_model(Article)
    def snapshot():
        return [dict(title=a.title, hidden=a.hidden,
                     labels=list(a.labels.order_by('code').values_list('code', flat=True)),
                     reviewers=list(a.reviewers.order_by('code').values_list('code', flat=True)))
                for a in Article.objects.order_by('pk')]
    results = []
    names = ['mixed', 'reordered', 'unchanged', 'order_only', 'collection_only', 'post_clean_only',
             'deferred', 'deferred_delete', 'new_repeat', 'late_failure', 'late_failure_atomic',
             'collection_failure', 'collection_failure_atomic']
    for name in names:
        Article.objects.all().delete()
        Label.objects.all().delete()
        labels = {code: Label.objects.create(code=code) for code in ['a', 'b', 'c']}
        for i, title in enumerate(['one', 'two', 'three'], 1):
            a = Article.objects.create(pk=i, title=title, hidden='stored')
            a.labels.set([labels['a']])
            a.reviewers.set([labels['a']])
        rows = [dict(id='1', title='changed', labels=['b'], reviewers=['c']),
                dict(id='2', title='two', labels=['a'], reviewers=['a'], DELETE='on'),
                dict(id='3', title='three', labels=['a'], reviewers=['a']),
                dict(title='new', labels=['b'], reviewers=['c']), dict(title=''),
                dict(title='discard', DELETE='on')]
        if name == 'new_repeat':
            rows[0] = dict(id='1', title='one', labels=['a'], reviewers=['a'])
            rows[1].pop('DELETE')
        if name == 'reordered':
            rows[0], rows[1] = rows[1], rows[0]
        if name in ['unchanged', 'order_only', 'collection_only', 'post_clean_only']:
            rows = [dict(id=str(i), title=title, labels=['a'], reviewers=['a'])
                    for i, title in enumerate(['one', 'two', 'three'], 1)]
        if name == 'order_only':
            for i, row in enumerate(rows):
                row['ORDER'] = str(3-i)
        if name == 'collection_only':
            rows[0]['labels'] = ['b']
        data = {'items-TOTAL_FORMS': str(len(rows)), 'items-INITIAL_FORMS': '3'}
        for i, row in enumerate(rows):
            for field, value in row.items():
                if field in ['labels', 'reviewers']:
                    value = [str(labels[code].pk) for code in value]
                data[f'items-{i}-{field}'] = value
        events = []
        class ProbeForm(forms.ModelForm):
            def clean(self):
                cleaned = super().clean()
                if name == 'post_clean_only':
                    self.instance.hidden = 'cleaned'
                return cleaned
        class ProbeSet(forms.BaseModelFormSet):
            def save_existing(self, form, instance, commit=True):
                if commit:
                    events.append([self.forms.index(form), 'update'])
                return super().save_existing(form, instance, commit)
            def delete_existing(self, instance, commit=True):
                if commit:
                    events.append([next(i for i, f in enumerate(self.forms) if f.instance is instance), 'delete'])
                return super().delete_existing(instance, commit)
            def save_new(self, form, commit=True):
                if commit:
                    events.append([self.forms.index(form), 'create'])
                result = super().save_new(form, commit)
                if commit and name.startswith('late_failure'):
                    raise RuntimeError('synthetic late writer failure')
                return result
        Set = forms.modelformset_factory(Article, form=ProbeForm, formset=ProbeSet,
                fields=['title', 'labels', 'reviewers'], can_delete=True, can_order=name=='order_only', extra=3)
        formset = Set(data, queryset=Article.objects.order_by('pk'), prefix='items')
        assert formset.is_valid(), formset.errors
        before = snapshot()
        with CaptureQueriesContext(connection) as queries:
            selected = formset.save(commit=False)
        selected_indexes = [next(i for i, f in enumerate(formset.forms) if f.instance is a) for a in selected]
        selected_changes = [formset.forms[i].changed_data for i in selected_indexes]
        deleted = [next(i for i, f in enumerate(formset.forms) if f.instance is a) for a in formset.deleted_objects]
        assert before == snapshot() and len(queries) == 0
        def collection_change(sender, instance, action, **kwargs):
            if name.startswith('collection_failure') and instance.title == 'new' and action == 'pre_add':
                raise RuntimeError('synthetic late collection failure')
        m2m_changed.connect(collection_change, sender=Article.reviewers.through)
        error = None
        def save():
            if name.startswith('deferred'):
                if name == 'deferred_delete':
                    for a in formset.deleted_objects:
                        a.delete()
                for a in selected:
                    a.title = 'server-' + a.title
                    a.save()
                formset.save_m2m()
            else:
                formset.save()
                if name == 'new_repeat':
                    formset.save()
        try:
            if name.endswith('_atomic'):
                with transaction.atomic():
                    save()
            else:
                save()
        except RuntimeError:
            error = 'RuntimeError'
        finally:
            m2m_changed.disconnect(collection_change, sender=Article.reviewers.through)
        results.append(dict(name=name, rows=rows, selected=selected_indexes, changed=selected_changes,
                            deleted=deleted, prepare_queries=len(queries), events=events, error=error,
                            has_keys=[a.pk is not None for a in selected], stored=snapshot()))
    result = dict(django=django.get_version(), python=platform.python_version(), backend='sqlite',
                  source_sha256=hashlib.sha256(Path(inspect.getsourcefile(form_models)).read_bytes()).hexdigest(), cases=results)
    print(json.dumps(result, indent=2, sort_keys=True))
