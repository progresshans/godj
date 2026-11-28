# Independently authored behavioral observer, not translated Django source.
# Reference: Django 6.1, fe0a859f537d4238cf49fca39073513206f83122,
# django/forms/models.py BaseModelForm.save/_save_m2m and
# django/db/models/fields/related_descriptors.py create_forward_many_to_many_manager.
# Upstream license: BSD-3-Clause, ../../../../LICENSE.django.
from pathlib import Path
import hashlib, inspect, json, os, platform, sqlite3, sys, tempfile
import django
from django.conf import settings

assert django.get_version() == '6.1'
assert platform.python_version() == '3.14.3'

with tempfile.TemporaryDirectory(prefix='godj-native-form-save-') as directory:
    root = Path(directory)
    app = root / 'saveprobe'
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
    database = json.loads(os.environ['GODJ_NATIVE_DATABASE']) if os.environ.get('GODJ_NATIVE_DATABASE') else {'ENGINE': 'django.db.backends.sqlite3', 'NAME': str(root / 'native.sqlite3')}
    settings.configure(SECRET_KEY='synthetic-form-save', INSTALLED_APPS=['saveprobe'], USE_I18N=False, USE_TZ=True, DATABASES={'default': database})
    django.setup()
    from django import forms
    from django.db import connection, connections, transaction
    from django.db.models import base as model_base
    from django.db.models.fields import related_descriptors
    from django.db.models.signals import m2m_changed
    from django.forms import models as form_models
    from django.test.utils import CaptureQueriesContext
    from saveprobe.models import Article, Label

    def snapshot():
        return [{'title': article.title, 'hidden': article.hidden,
                 'labels': list(article.labels.order_by('code').values_list('code', flat=True)),
                 'reviewers': list(article.reviewers.order_by('code').values_list('code', flat=True))}
                for article in Article.objects.order_by('pk')]

    names = ['new_save', 'new_repeat', 'new_deferred', 'new_deferred_unsaved_m2m',
             'existing_replace', 'existing_clear', 'existing_excluded', 'invalid_scalar', 'invalid_choice',
             'new_collection_failure', 'new_collection_failure_atomic',
             'existing_collection_failure', 'existing_collection_failure_atomic',
             'new_late_deleted_choice', 'new_late_deleted_choice_atomic']
    with connection.schema_editor() as editor:
        editor.create_model(Label)
        editor.create_model(Article)
    observations = []
    try:
        for name in names:
            Article.objects.all().delete()
            Label.objects.all().delete()
            labels = {code: Label.objects.create(code=code) for code in ['a', 'b', 'c']}
            current = None
            if name.startswith('existing_'):
                current = Article.objects.create(title='old', hidden='stored')
                current.labels.set([labels['a']])
                current.reviewers.set([labels['a']])
            fields = ['title'] if name == 'existing_excluded' else ['title', 'labels', 'reviewers']
            Form = forms.modelform_factory(Article, fields=fields)
            data = {'title': 'changed', 'labels': [str(labels['b'].pk)], 'reviewers': [str(labels['c'].pk)]}
            if name == 'existing_clear':
                data['labels'] = []
                data['reviewers'] = []
            if name == 'invalid_scalar':
                data['title'] = ''
            if name == 'invalid_choice':
                data['labels'] = ['999999999']
            form = Form(data=data, instance=current)
            valid = form.is_valid()
            before = snapshot()
            prepared = None
            with CaptureQueriesContext(connection) as queries:
                try:
                    prepared = form.save(commit=False)
                    preparation = {'ok': True, 'same_instance': prepared is form.instance, 'has_key': prepared.pk is not None}
                except Exception as error:
                    preparation = {'ok': False, 'error': type(error).__name__}
            preparation['query_count'] = len(queries)
            assert snapshot() == before
            events = []
            def observe_change(sender, instance, action, **kwargs):
                if action == 'pre_add':
                    field = 'labels' if sender is Article.labels.through else 'reviewers'
                    events.append(field)
                    if 'collection_failure' in name and field == 'reviewers':
                        raise RuntimeError('synthetic second collection failure')
            for field in [Article.labels, Article.reviewers]:
                m2m_changed.connect(observe_change, sender=field.through)
            unsaved = None
            if name == 'new_deferred_unsaved_m2m':
                with CaptureQueriesContext(connection) as queries:
                    try:
                        form.save_m2m()
                        unsaved = {'ok': True}
                    except Exception as error:
                        unsaved = {'ok': False, 'error': type(error).__name__}
                unsaved['query_count'] = len(queries)
            if 'late_deleted_choice' in name:
                labels['b'].delete()
            try:
                def save():
                    if 'deferred' in name:
                        prepared.title = 'server-edited'
                        prepared.save()
                        form.save_m2m()
                        return prepared
                    result = form.save()
                    if name == 'new_repeat':
                        assert form.save() is result
                    return result
                if name.endswith('_atomic'):
                    with transaction.atomic():
                        result = save()
                else:
                    result = save()
                saved = {'ok': True, 'same_instance': result is form.instance}
            except Exception as error:
                saved = {'ok': False, 'error': type(error).__name__}
            finally:
                for field in [Article.labels, Article.reviewers]:
                    m2m_changed.disconnect(observe_change, sender=field.through)
            saved['has_key'] = form.instance.pk is not None
            observations.append({'name': name, 'valid': valid,
                'errors': {field: [error.code for error in errors] for field, errors in form.errors.as_data().items()},
                'prepare': preparation, 'unsaved_collections': unsaved,
                'collection_events': events, 'save': saved, 'stored': snapshot()})
    finally:
        with connection.schema_editor() as editor:
            editor.delete_model(Article)
            editor.delete_model(Label)
        assert not connection.introspection.table_names()
        connections.close_all()
    print(json.dumps({'django': django.get_version(), 'django_commit': 'fe0a859f537d4238cf49fca39073513206f83122',
        'python': platform.python_version(), 'platform': platform.system().lower(), 'architecture': platform.machine(),
        'backend': database['ENGINE'].removeprefix('django.db.backends.'), 'sqlite_version': sqlite3.sqlite_version,
        'source_sha256': {name: hashlib.sha256(Path(inspect.getfile(module)).read_bytes()).hexdigest() for name, module in {'model_base': model_base, 'form_models': form_models, 'related_descriptors': related_descriptors}.items()},
        'scope': 'native ModelForm scalar and selected collection save ordering, deferred save, partial failure and explicit atomic rollback',
        'cases': observations, 'cleanup_empty': True}, sort_keys=True, indent=2))
